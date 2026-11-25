/*
 * tvplay — the video player Rabbit Ears uses on the RG Nano (DrUm78 FunKey-OS).
 *
 * DrUm78's OS has no video player, so this is a small one, built statically
 * with FFmpeg (decode only) and alsa-lib. It draws straight to /dev/fb0
 * (240x240 RGB565 on the Nano) and plays sound through ALSA's "default"
 * device, so the OS's own asound.conf (mono speaker, USB-C audio) and volume
 * controls keep working.
 *
 *   tvplay [-ss SEC] [-t SEC] [-osd FILE -osd-from SEC -osd-to SEC]
 *          [-fill] [-dvr] [-pos FILE] [-fb DEV] [-fbsize WxH] [-input DEV]
 *          [-adev ALSA] [-noaudio] FILE
 *
 *   -ss      start this many seconds into the file
 *   -t       stop after playing this many seconds (a commercial break is due)
 *   -osd     a "RBOV" overlay (u16 w, u16 h, RGBA) shown while the playing
 *            position is between -osd-from and -osd-to
 *   -fill    crop to fill the square screen instead of letterboxing 4:3
 *   -dvr     on-demand mode: the d-pad seeks, A pauses, L/R jump 10 minutes
 *   -pos     write the playing position here (every second and at exit)
 *   -fb      framebuffer device, or a plain file (then -fbsize, RGB565)
 *
 * Exit status 0 at the end of the file or the -t limit, or on SIGTERM;
 * 1 with a one-line reason on stderr when the file can't be played.
 */

#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/fb.h>
#include <math.h>
#include <pthread.h>
#include <signal.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#include <alsa/asoundlib.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/channel_layout.h>
#include <libavutil/opt.h>
#include <libavutil/pixdesc.h>
#include <libswresample/swresample.h>

/* ---------- options ---------- */

static double opt_ss = 0, opt_t = 0, osd_from = 0, osd_to = -1;
static const char *opt_osd, *opt_pos, *opt_file, *opt_input;
static const char *opt_fb = "/dev/fb0", *opt_adev = "default";
static int opt_fill, opt_dvr, opt_noaudio, opt_fbw = 240, opt_fbh = 240;

static volatile sig_atomic_t quit_flag;

static void die(const char *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	vfprintf(stderr, fmt, ap);
	va_end(ap);
	fputc('\n', stderr);
	exit(1);
}

static double now_s(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec + ts.tv_nsec / 1e9;
}

static void sleep_s(double s) {
	if (s <= 0)
		return;
	struct timespec ts = {(time_t)s, (long)((s - (time_t)s) * 1e9)};
	nanosleep(&ts, NULL);
}

/* ---------- screen ---------- */

static struct {
	uint8_t *mem;
	int w, h, bpp, stride;
	uint16_t *back; /* w*h RGB565, composed here then copied */
	int rx, ry, rw, rh; /* where the picture goes */
} scr;

static void fb_open(void) {
	int fd = open(opt_fb, O_RDWR | (strncmp(opt_fb, "/dev/", 5) ? O_CREAT : 0), 0644);
	if (fd < 0)
		die("can't open %s: %s", opt_fb, strerror(errno));
	struct fb_var_screeninfo v;
	struct fb_fix_screeninfo f;
	if (ioctl(fd, FBIOGET_VSCREENINFO, &v) == 0 && ioctl(fd, FBIOGET_FSCREENINFO, &f) == 0) {
		scr.w = v.xres;
		scr.h = v.yres;
		scr.bpp = v.bits_per_pixel;
		scr.stride = f.line_length;
		if (v.yoffset || v.xoffset) {
			v.xoffset = v.yoffset = 0;
			ioctl(fd, FBIOPAN_DISPLAY, &v);
		}
	} else { /* a plain file, for testing */
		scr.w = opt_fbw;
		scr.h = opt_fbh;
		scr.bpp = 16;
		scr.stride = scr.w * 2;
		if (ftruncate(fd, (off_t)scr.stride * scr.h) != 0)
			die("can't size %s", opt_fb);
	}
	if (scr.bpp != 16 && scr.bpp != 32)
		die("unsupported framebuffer depth %d", scr.bpp);
	scr.mem = mmap(NULL, (size_t)scr.stride * scr.h, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
	if (scr.mem == MAP_FAILED)
		die("can't map %s: %s", opt_fb, strerror(errno));
	close(fd);
	scr.back = calloc((size_t)scr.w * scr.h, 2);
}

/* copy rows [y0,y1) of the back buffer to the panel */
static void fb_flush(int y0, int y1) {
	if (y0 < 0)
		y0 = 0;
	if (y1 > scr.h)
		y1 = scr.h;
	for (int y = y0; y < y1; y++) {
		uint16_t *s = scr.back + (size_t)y * scr.w;
		uint8_t *d = scr.mem + (size_t)y * scr.stride;
		if (scr.bpp == 16) {
			memcpy(d, s, (size_t)scr.w * 2);
		} else {
			uint32_t *d32 = (uint32_t *)d;
			for (int x = 0; x < scr.w; x++) {
				uint16_t p = s[x];
				uint32_t r = (p >> 11) & 31, g = (p >> 5) & 63, b = p & 31;
				d32[x] = 0xff000000u | (r << 19 | (r >> 2) << 16) | (g << 10 | (g >> 4) << 8) | (b << 3 | b >> 2);
			}
		}
	}
}

static inline uint16_t rgb565(int r, int g, int b) { return (uint16_t)((r >> 3) << 11 | (g >> 2) << 5 | (b >> 3)); }

/* ---------- overlay (the channel number Rabbit Ears drew) ---------- */

static struct {
	int n;
	uint32_t *at;  /* pixel index into the back buffer */
	uint16_t *col; /* its colour */
	int y0, y1;    /* rows it touches */
} osd;

static void osd_load(const char *path) {
	FILE *f = fopen(path, "rb");
	if (!f)
		return;
	uint8_t hdr[8];
	if (fread(hdr, 1, 8, f) != 8 || memcmp(hdr, "RBOV", 4) != 0) {
		fclose(f);
		return;
	}
	int w = hdr[4] | hdr[5] << 8, h = hdr[6] | hdr[7] << 8;
	uint8_t *px = malloc((size_t)w * h * 4);
	if (!px || fread(px, 4, (size_t)w * h, f) != (size_t)w * h) {
		free(px);
		fclose(f);
		return;
	}
	fclose(f);
	int cap = 0;
	osd.y0 = scr.h;
	osd.y1 = 0;
	for (int y = 0; y < h && y < scr.h; y++)
		for (int x = 0; x < w && x < scr.w; x++) {
			uint8_t *p = px + ((size_t)y * w + x) * 4;
			if (p[3] < 128)
				continue;
			if (osd.n == cap) {
				cap = cap ? cap * 2 : 1024;
				osd.at = realloc(osd.at, cap * sizeof *osd.at);
				osd.col = realloc(osd.col, cap * sizeof *osd.col);
			}
			osd.at[osd.n] = (uint32_t)(y * scr.w + x);
			osd.col[osd.n] = rgb565(p[0], p[1], p[2]);
			osd.n++;
			if (y < osd.y0)
				osd.y0 = y;
			if (y + 1 > osd.y1)
				osd.y1 = y + 1;
		}
	free(px);
}

static void osd_apply(void) {
	for (int i = 0; i < osd.n; i++)
		scr.back[osd.at[i]] = osd.col[i];
}

/* ---------- a tiny HUD for DVR seeking: bar, time, pause ---------- */

static const uint8_t digits[13][5] = {
	/* 3x5 glyphs: 0-9, ':', '/', ' ' */
	{7, 5, 5, 5, 7}, {2, 6, 2, 2, 7}, {7, 1, 7, 4, 7}, {7, 1, 3, 1, 7}, {5, 5, 7, 1, 1}, {7, 4, 7, 1, 7},
	{7, 4, 7, 5, 7}, {7, 1, 1, 2, 2}, {7, 5, 7, 5, 7}, {7, 5, 7, 1, 7}, {0, 2, 0, 2, 0}, {1, 1, 2, 4, 4}, {0, 0, 0, 0, 0},
};

static void hud_rect(int x, int y, int w, int h, uint16_t c) {
	for (int yy = y; yy < y + h; yy++) {
		if (yy < 0 || yy >= scr.h)
			continue;
		for (int xx = x; xx < x + w; xx++)
			if (xx >= 0 && xx < scr.w)
				scr.back[yy * scr.w + xx] = c;
	}
}

static int hud_text(int x, int y, const char *s, int k, uint16_t c) {
	for (; *s; s++) {
		int g = *s == ':' ? 10 : *s == '/' ? 11 : (*s >= '0' && *s <= '9') ? *s - '0' : 12;
		for (int r = 0; r < 5; r++)
			for (int b = 0; b < 3; b++)
				if (digits[g][r] & (4 >> b))
					hud_rect(x + b * k, y + r * k, k, k, c);
		x += 4 * k;
	}
	return x;
}

static void fmt_time(char *out, double t) {
	int s = t < 0 ? 0 : (int)(t + 0.5);
	if (s >= 3600)
		sprintf(out, "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60);
	else
		sprintf(out, "%d:%02d", s / 60, s % 60);
}

/* draws the HUD into the back buffer; returns the first row it used */
static int hud_draw(double pos, double dur, int paused) {
	uint16_t green = rgb565(0x52, 0xff, 0x4a), dark = rgb565(0x0c, 0x30, 0x0c), black = 0;
	int k = 2, h = 26, y = scr.h - h;
	hud_rect(0, y, scr.w, h, black);
	char a[16], b[16], line[40];
	fmt_time(a, pos);
	fmt_time(b, dur);
	snprintf(line, sizeof line, dur > 0 ? "%s / %s" : "%s", a, b);
	int x = 8;
	if (paused) {
		hud_rect(x, y + 4, 3, 10, green);
		hud_rect(x + 6, y + 4, 3, 10, green);
	} else {
		for (int i = 0; i < 8; i++)
			hud_rect(x + i, y + 4 + i / 2, 1, 10 - i / 2 * 2, green);
	}
	hud_text(x + 16, y + 4, line, k, green);
	if (dur > 0) {
		int bw = scr.w - 16;
		double f = pos / dur;
		if (f < 0)
			f = 0;
		if (f > 1)
			f = 1;
		hud_rect(8, y + 18, bw, 4, dark);
		hud_rect(8, y + 18, (int)(bw * f), 4, green);
	}
	return y;
}

/* ---------- packet queues ---------- */

#define PKT_FLUSH ((AVPacket *)1)
#define PKT_EOF ((AVPacket *)2)

typedef struct {
	AVPacket *v[512];
	int head, n, bytes;
	pthread_mutex_t mu;
	pthread_cond_t cv;
} Queue;

static void q_init(Queue *q) {
	memset(q, 0, sizeof *q);
	pthread_mutex_init(&q->mu, NULL);
	pthread_cond_init(&q->cv, NULL);
}

static int q_full(Queue *q) { return q->n >= 500 || q->bytes > 3 << 20; }

static void q_put(Queue *q, AVPacket *p) {
	pthread_mutex_lock(&q->mu);
	while (q_full(q) && !quit_flag) {
		struct timespec ts;
		clock_gettime(CLOCK_REALTIME, &ts);
		ts.tv_nsec += 50000000;
		if (ts.tv_nsec >= 1000000000) {
			ts.tv_sec++;
			ts.tv_nsec -= 1000000000;
		}
		pthread_cond_timedwait(&q->cv, &q->mu, &ts);
	}
	q->v[(q->head + q->n) % 512] = p;
	q->n++;
	if (p != PKT_FLUSH && p != PKT_EOF)
		q->bytes += p->size;
	pthread_cond_broadcast(&q->cv);
	pthread_mutex_unlock(&q->mu);
}

/* returns NULL on timeout */
static AVPacket *q_get(Queue *q, double timeout) {
	pthread_mutex_lock(&q->mu);
	double end = now_s() + timeout;
	while (q->n == 0 && !quit_flag) {
		double left = end - now_s();
		if (left <= 0)
			break;
		struct timespec ts;
		clock_gettime(CLOCK_REALTIME, &ts);
		long ns = (long)(left * 1e9);
		if (ns > 50000000)
			ns = 50000000;
		ts.tv_nsec += ns;
		if (ts.tv_nsec >= 1000000000) {
			ts.tv_sec++;
			ts.tv_nsec -= 1000000000;
		}
		pthread_cond_timedwait(&q->cv, &q->mu, &ts);
	}
	AVPacket *p = NULL;
	if (q->n > 0) {
		p = q->v[q->head];
		q->head = (q->head + 1) % 512;
		q->n--;
		if (p != PKT_FLUSH && p != PKT_EOF)
			q->bytes -= p->size;
		pthread_cond_broadcast(&q->cv);
	}
	pthread_mutex_unlock(&q->mu);
	return p;
}

static void q_clear(Queue *q) {
	pthread_mutex_lock(&q->mu);
	while (q->n > 0) {
		AVPacket *p = q->v[q->head];
		q->head = (q->head + 1) % 512;
		q->n--;
		if (p != PKT_FLUSH && p != PKT_EOF)
			av_packet_free(&p);
	}
	q->bytes = 0;
	pthread_cond_broadcast(&q->cv);
	pthread_mutex_unlock(&q->mu);
}

/* ---------- shared state ---------- */

static AVFormatContext *fmt;
static int vidx = -1, aidx = -1;
static AVCodecContext *vdec, *adec;
static Queue vq, aq;
static double duration;

static pthread_mutex_t st_mu = PTHREAD_MUTEX_INITIALIZER;
static double seek_target = -1;   /* demuxer: seek here (seconds) */
static double discard_before = 0; /* decoders: drop frames before this */
static int serial;                /* bumped on every seek */
static int paused;
static int demux_eof;

/* the audio clock: the position being heard right now */
static int have_audio;
static int aclock_ok;
static double aclock_pts, aclock_at;
static int audio_done;

/* system clock when there's no audio */
static double sclock_base = -1; /* now_s() - pts */
static double pause_started;

static double master_clock(void) {
	pthread_mutex_lock(&st_mu);
	double c;
	if (have_audio) {
		if (!aclock_ok)
			c = NAN;
		else if (paused)
			c = aclock_pts;
		else {
			double ext = now_s() - aclock_at;
			if (ext > 0.25)
				ext = 0.25; /* audio stalled: don't run away */
			c = aclock_pts + ext;
		}
	} else if (sclock_base < 0)
		c = NAN;
	else
		c = (paused ? pause_started : now_s()) - sclock_base;
	pthread_mutex_unlock(&st_mu);
	return c;
}

/* ---------- demuxer ---------- */

static void *demux_main(void *arg) {
	(void)arg;
	AVPacket *pkt = av_packet_alloc();
	while (!quit_flag) {
		pthread_mutex_lock(&st_mu);
		double target = seek_target;
		seek_target = -1;
		pthread_mutex_unlock(&st_mu);
		if (target >= 0) {
			int64_t ts = (int64_t)(target * AV_TIME_BASE);
			if (fmt->start_time != AV_NOPTS_VALUE)
				ts += fmt->start_time;
			avformat_seek_file(fmt, -1, INT64_MIN, ts, ts, 0);
			q_clear(&vq);
			q_clear(&aq);
			q_put(&vq, PKT_FLUSH);
			if (aidx >= 0)
				q_put(&aq, PKT_FLUSH);
			pthread_mutex_lock(&st_mu);
			demux_eof = 0;
			pthread_mutex_unlock(&st_mu);
		}
		if (demux_eof) {
			sleep_s(0.02);
			continue;
		}
		int r = av_read_frame(fmt, pkt);
		if (r < 0) {
			q_put(&vq, PKT_EOF);
			if (aidx >= 0)
				q_put(&aq, PKT_EOF);
			pthread_mutex_lock(&st_mu);
			demux_eof = 1;
			pthread_mutex_unlock(&st_mu);
			continue;
		}
		if (pkt->stream_index == vidx) {
			q_put(&vq, av_packet_clone(pkt));
		} else if (pkt->stream_index == aidx) {
			q_put(&aq, av_packet_clone(pkt));
		}
		av_packet_unref(pkt);
	}
	av_packet_free(&pkt);
	return NULL;
}

/* ---------- audio ---------- */

static snd_pcm_t *pcm;
static unsigned out_rate;
static int out_ch = 2;
static SwrContext *swr;

static int audio_open(void) {
	if (opt_noaudio || aidx < 0)
		return 0;
	const char *dev = getenv("TVPLAY_ALSA");
	if (!dev)
		dev = opt_adev;
	snd_lib_error_set_handler(NULL);
	if (snd_pcm_open(&pcm, dev, SND_PCM_STREAM_PLAYBACK, 0) < 0) {
		fprintf(stderr, "no sound: can't open ALSA device %s\n", dev);
		pcm = NULL;
		return 0;
	}
	snd_pcm_hw_params_t *hw;
	snd_pcm_hw_params_alloca(&hw);
	unsigned rate = adec->sample_rate, ch = 2, btime = 120000, ptime = 30000;
	int dir = 0;
	if (snd_pcm_hw_params_any(pcm, hw) < 0 ||
	    snd_pcm_hw_params_set_access(pcm, hw, SND_PCM_ACCESS_RW_INTERLEAVED) < 0 ||
	    snd_pcm_hw_params_set_format(pcm, hw, SND_PCM_FORMAT_S16_LE) < 0 ||
	    snd_pcm_hw_params_set_channels_near(pcm, hw, &ch) < 0 ||
	    snd_pcm_hw_params_set_rate_near(pcm, hw, &rate, &dir) < 0) {
		fprintf(stderr, "no sound: ALSA won't take S16 audio\n");
		snd_pcm_close(pcm);
		pcm = NULL;
		return 0;
	}
	snd_pcm_hw_params_set_buffer_time_near(pcm, hw, &btime, &dir);
	snd_pcm_hw_params_set_period_time_near(pcm, hw, &ptime, &dir);
	if (snd_pcm_hw_params(pcm, hw) < 0) {
		fprintf(stderr, "no sound: ALSA setup failed\n");
		snd_pcm_close(pcm);
		pcm = NULL;
		return 0;
	}
	out_rate = rate;
	out_ch = (int)ch;
	AVChannelLayout ol;
	av_channel_layout_default(&ol, out_ch);
	if (swr_alloc_set_opts2(&swr, &ol, AV_SAMPLE_FMT_S16, (int)out_rate, &adec->ch_layout, adec->sample_fmt,
	                        adec->sample_rate, 0, NULL) < 0 ||
	    swr_init(swr) < 0) {
		fprintf(stderr, "no sound: can't convert this audio\n");
		snd_pcm_close(pcm);
		pcm = NULL;
		return 0;
	}
	return 1;
}

static void set_aclock(double pts_end) {
	snd_pcm_sframes_t d = 0;
	if (snd_pcm_delay(pcm, &d) < 0 || d < 0)
		d = 0;
	pthread_mutex_lock(&st_mu);
	aclock_pts = pts_end - (double)d / out_rate;
	aclock_at = now_s();
	aclock_ok = 1;
	pthread_mutex_unlock(&st_mu);
}

static void *audio_main(void *arg) {
	(void)arg;
	AVFrame *fr = av_frame_alloc();
	int16_t *buf = NULL;
	int bufcap = 0;
	int was_paused = 0, eof = 0;
	double tb = av_q2d(fmt->streams[aidx]->time_base);
	double start = fmt->streams[aidx]->start_time != AV_NOPTS_VALUE ? fmt->streams[aidx]->start_time * tb : 0;
	double next_pts = 0;
	while (!quit_flag) {
		pthread_mutex_lock(&st_mu);
		int p = paused;
		double disc = discard_before;
		pthread_mutex_unlock(&st_mu);
		if (p) {
			if (!was_paused)
				snd_pcm_drop(pcm);
			was_paused = 1;
			sleep_s(0.02);
			continue;
		}
		if (was_paused) {
			snd_pcm_prepare(pcm);
			was_paused = 0;
		}
		if (eof) {
			sleep_s(0.02);
			continue;
		}
		AVPacket *pkt = q_get(&aq, 0.1);
		if (!pkt)
			continue;
		if (pkt == PKT_FLUSH) {
			avcodec_flush_buffers(adec);
			snd_pcm_drop(pcm);
			snd_pcm_prepare(pcm);
			pthread_mutex_lock(&st_mu);
			aclock_ok = 0;
			audio_done = 0;
			pthread_mutex_unlock(&st_mu);
			eof = 0;
			continue;
		}
		if (pkt == PKT_EOF) {
			avcodec_send_packet(adec, NULL);
		} else {
			avcodec_send_packet(adec, pkt);
			av_packet_free(&pkt);
		}
		for (;;) {
			int r = avcodec_receive_frame(adec, fr);
			if (r < 0)
				break;
			double pts = fr->best_effort_timestamp != AV_NOPTS_VALUE ? fr->best_effort_timestamp * tb - start : next_pts;
			double fdur = (double)fr->nb_samples / fr->sample_rate;
			next_pts = pts + fdur;
			if (pts + fdur < disc) {
				av_frame_unref(fr);
				continue;
			}
			int max = swr_get_out_samples(swr, fr->nb_samples);
			if (max * out_ch > bufcap) {
				bufcap = max * out_ch;
				buf = realloc(buf, bufcap * 2);
			}
			uint8_t *o = (uint8_t *)buf;
			int n = swr_convert(swr, &o, max, (const uint8_t **)fr->extended_data, fr->nb_samples);
			av_frame_unref(fr);
			int16_t *w = buf;
			while (n > 0 && !quit_flag) {
				snd_pcm_sframes_t k = snd_pcm_writei(pcm, w, n);
				if (k == -EAGAIN)
					continue;
				if (k < 0) {
					if (snd_pcm_recover(pcm, (int)k, 1) < 0)
						break;
					continue;
				}
				w += k * out_ch;
				n -= (int)k;
			}
			set_aclock(next_pts);
		}
		if (pkt == PKT_EOF) {
			eof = 1;
			snd_pcm_drain(pcm);
			pthread_mutex_lock(&st_mu);
			audio_done = 1;
			pthread_mutex_unlock(&st_mu);
		}
	}
	av_frame_free(&fr);
	free(buf);
	return NULL;
}

/* ---------- picture ---------- */

static int clip_tab[1024];
#define CLIP(v) clip_tab[((v) >> 8) + 384]

static int *xmap, *xfrac, *ymap, *yfrac; /* per output column/row: source and blend weight (0..256) */
static int src_w, src_h, src_pixfmt, cshx, cshy;

static void picture_setup(int w, int h, AVRational sar, int pixfmt) {
	src_w = w;
	src_h = h;
	src_pixfmt = pixfmt;
	const AVPixFmtDescriptor *d = av_pix_fmt_desc_get(pixfmt);
	if (!d || (d->flags & (AV_PIX_FMT_FLAG_RGB | AV_PIX_FMT_FLAG_PAL)) || d->nb_components < 3 || d->comp[0].depth != 8 ||
	    !(d->flags & AV_PIX_FMT_FLAG_PLANAR))
		die("unsupported video format %s: convert it with vhsify", d ? d->name : "?");
	cshx = d->log2_chroma_w;
	cshy = d->log2_chroma_h;
	double a = (double)w / h;
	if (sar.num > 0 && sar.den > 0)
		a *= (double)sar.num / sar.den;
	/* source rectangle (crop for -fill) and screen rectangle */
	double sx0 = 0, sy0 = 0, sw = w, sh = h;
	double sa = (double)scr.w / scr.h;
	if (opt_fill) {
		scr.rx = 0, scr.ry = 0, scr.rw = scr.w, scr.rh = scr.h;
		if (a > sa) { /* wider: crop the sides */
			sw = w * sa / a;
			sx0 = (w - sw) / 2;
		} else {
			sh = h * a / sa;
			sy0 = (h - sh) / 2;
		}
	} else if (a > sa) {
		scr.rw = scr.w;
		scr.rh = (int)(scr.w / a + 0.5) & ~1;
		scr.rx = 0;
		scr.ry = (scr.h - scr.rh) / 2;
	} else {
		scr.rh = scr.h;
		scr.rw = (int)(scr.h * a + 0.5) & ~1;
		scr.ry = 0;
		scr.rx = (scr.w - scr.rw) / 2;
	}
	free(xmap), free(xfrac), free(ymap), free(yfrac);
	xmap = malloc(scr.rw * sizeof(int));
	xfrac = malloc(scr.rw * sizeof(int));
	ymap = malloc(scr.rh * sizeof(int));
	yfrac = malloc(scr.rh * sizeof(int));
	for (int x = 0; x < scr.rw; x++) {
		double s = sx0 + (x + 0.5) * sw / scr.rw - 0.5;
		if (s < 0)
			s = 0;
		if (s > w - 1.001)
			s = w - 1.001;
		xmap[x] = (int)s;
		xfrac[x] = (int)((s - (int)s) * 256);
	}
	for (int y = 0; y < scr.rh; y++) {
		double s = sy0 + (y + 0.5) * sh / scr.rh - 0.5;
		if (s < 0)
			s = 0;
		if (s > h - 1.001)
			s = h - 1.001;
		ymap[y] = (int)s;
		yfrac[y] = (int)((s - (int)s) * 256);
	}
	for (int i = 0; i < 1024; i++) {
		int v = i - 384;
		clip_tab[i] = v < 0 ? 0 : v > 255 ? 255 : v;
	}
	memset(scr.back, 0, (size_t)scr.w * scr.h * 2);
}

/* YUV (bilinear luma, nearest chroma) → RGB565 into the picture rectangle */
static void picture_draw(const AVFrame *f) {
	int full = f->color_range == AVCOL_RANGE_JPEG || src_pixfmt == AV_PIX_FMT_YUVJ420P || src_pixfmt == AV_PIX_FMT_YUVJ422P ||
	           src_pixfmt == AV_PIX_FMT_YUVJ444P;
	int ky = full ? 256 : 298, oy = full ? 0 : 16;
	int kr = full ? 359 : 409, kgu = full ? 88 : 100, kgv = full ? 183 : 208, kb = full ? 454 : 516;
	for (int y = 0; y < scr.rh; y++) {
		int sy = ymap[y], fy = yfrac[y];
		const uint8_t *r0 = f->data[0] + (size_t)sy * f->linesize[0];
		const uint8_t *r1 = r0 + (sy + 1 < src_h ? f->linesize[0] : 0);
		int cy = (sy + (fy >= 128)) >> cshy;
		if (cy > ((src_h - 1) >> cshy))
			cy = (src_h - 1) >> cshy;
		const uint8_t *u = f->data[1] + (size_t)cy * f->linesize[1];
		const uint8_t *v = f->data[2] + (size_t)cy * f->linesize[2];
		uint16_t *out = scr.back + (size_t)(scr.ry + y) * scr.w + scr.rx;
		for (int x = 0; x < scr.rw; x++) {
			int sx = xmap[x], fx = xfrac[x];
			int a = r0[sx], b = r0[sx + 1], c = r1[sx], d = r1[sx + 1];
			int top = a * 256 + (b - a) * fx, bot = c * 256 + (d - c) * fx;
			int Y = (top * 256 + (bot - top) * fy) >> 16;
			int cx = (sx + (fx >= 128)) >> cshx;
			int U = u[cx] - 128, V = v[cx] - 128;
			int C = (Y - oy) * ky + 128;
			int R = CLIP(C + kr * V), G = CLIP(C - kgu * U - kgv * V), B = CLIP(C + kb * U);
			out[x] = rgb565(R, G, B);
		}
	}
}

/* ---------- input (DVR mode) ---------- */

/* fkgpiod's key map (see /etc/fkgpiod.conf) */
enum { K_A = 30, K_B = 48, K_X = 45, K_Y = 21, K_UP = 22, K_DOWN = 32, K_LEFT = 38, K_RIGHT = 19, K_L = 50, K_R = 49 };

static int find_buttons(char *out, size_t n) {
	FILE *f = fopen("/proc/bus/input/devices", "r");
	if (!f)
		return 0;
	char line[512];
	int match = 0, found = 0;
	while (fgets(line, sizeof line, f)) {
		if (!strncmp(line, "N: Name=", 8))
			match = strstr(line, "ample\"") != NULL;
		if (match && !strncmp(line, "H: Handlers=", 12)) {
			char *e = strstr(line, "event");
			if (e) {
				int k = 0;
				sscanf(e, "event%d", &k);
				snprintf(out, n, "/dev/input/event%d", k);
				found = 1;
				break;
			}
		}
	}
	fclose(f);
	return found;
}

static void request_seek(double to) {
	if (duration > 0 && to > duration - 1)
		to = duration - 1;
	if (to < 0)
		to = 0;
	pthread_mutex_lock(&st_mu);
	seek_target = to;
	discard_before = to;
	serial++;
	pthread_mutex_unlock(&st_mu);
}

static double hud_until;
static double last_pos;

static void *input_main(void *arg) {
	(void)arg;
	char path[64];
	const char *dev = opt_input;
	if (!dev && find_buttons(path, sizeof path))
		dev = path;
	if (!dev)
		dev = "/dev/input/event0";
	int fd = open(dev, O_RDONLY);
	if (fd < 0)
		return NULL;
	/* struct input_event as a 32-bit kernel writes it: 16 bytes */
	uint8_t ev[16];
	while (!quit_flag) {
		if (read(fd, ev, sizeof ev) != (ssize_t)sizeof ev)
			break;
		uint16_t type = ev[8] | ev[9] << 8, code = ev[10] | ev[11] << 8;
		int32_t val = (int32_t)(ev[12] | ev[13] << 8 | ev[14] << 16 | (uint32_t)ev[15] << 24);
		if (type != 1 || val != 1)
			continue;
		double d = 0;
		switch (code) {
		case K_LEFT: d = -10; break;
		case K_RIGHT: d = 10; break;
		case K_DOWN: d = -60; break;
		case K_UP: d = 60; break;
		case K_L: d = -600; break;
		case K_R: d = 600; break;
		case K_A:
			pthread_mutex_lock(&st_mu);
			paused = !paused;
			if (paused) {
				pause_started = now_s();
			} else if (sclock_base >= 0) {
				sclock_base += now_s() - pause_started;
			}
			pthread_mutex_unlock(&st_mu);
			hud_until = paused ? 1e18 : now_s() + 2;
			continue;
		default:
			continue;
		}
		pthread_mutex_lock(&st_mu);
		double base = seek_target >= 0 ? seek_target : (discard_before > last_pos ? discard_before : last_pos);
		int was_paused = paused;
		paused = 0;
		pthread_mutex_unlock(&st_mu);
		(void)was_paused;
		request_seek(base + d);
		hud_until = now_s() + 2.5;
	}
	close(fd);
	return NULL;
}

/* ---------- position file ---------- */

static void write_pos(double pos) {
	if (!opt_pos)
		return;
	char tmp[512];
	snprintf(tmp, sizeof tmp, "%s.tmp", opt_pos);
	FILE *f = fopen(tmp, "w");
	if (!f)
		return;
	fprintf(f, "%.2f\n", pos);
	fclose(f);
	rename(tmp, opt_pos);
}

/* ---------- main ---------- */

static void on_signal(int s) {
	(void)s;
	quit_flag = 1;
}

static void usage(void) {
	die("usage: tvplay [-ss SEC] [-t SEC] [-osd FILE -osd-from SEC -osd-to SEC] [-fill] [-dvr] [-pos FILE] [-fb DEV] "
	    "[-fbsize WxH] [-input DEV] [-adev ALSA] [-noaudio] FILE");
}

int main(int argc, char **argv) {
	for (int i = 1; i < argc; i++) {
		const char *a = argv[i];
		const char *v = i + 1 < argc ? argv[i + 1] : NULL;
#define ARG(name) (!strcmp(a, name) && v && (i++, 1))
		if (ARG("-ss"))
			opt_ss = atof(v);
		else if (ARG("-t"))
			opt_t = atof(v);
		else if (ARG("-osd"))
			opt_osd = v;
		else if (ARG("-osd-from"))
			osd_from = atof(v);
		else if (ARG("-osd-to"))
			osd_to = atof(v);
		else if (ARG("-pos"))
			opt_pos = v;
		else if (ARG("-fb"))
			opt_fb = v;
		else if (ARG("-fbsize"))
			sscanf(v, "%dx%d", &opt_fbw, &opt_fbh);
		else if (ARG("-input"))
			opt_input = v;
		else if (ARG("-adev"))
			opt_adev = v;
		else if (!strcmp(a, "-fill"))
			opt_fill = 1;
		else if (!strcmp(a, "-dvr"))
			opt_dvr = 1;
		else if (!strcmp(a, "-noaudio"))
			opt_noaudio = 1;
		else if (a[0] == '-' && a[1])
			usage();
		else
			opt_file = a;
	}
	if (!opt_file)
		usage();

	struct sigaction sa = {0};
	sa.sa_handler = on_signal;
	sigaction(SIGTERM, &sa, NULL);
	sigaction(SIGINT, &sa, NULL);
	sigaction(SIGHUP, &sa, NULL);
	signal(SIGPIPE, SIG_IGN);
	av_log_set_level(AV_LOG_QUIET);

	if (avformat_open_input(&fmt, opt_file, NULL, NULL) < 0)
		die("can't open %s (is it an MP4?)", opt_file);
	if (avformat_find_stream_info(fmt, NULL) < 0)
		die("can't read %s", opt_file);
	duration = fmt->duration > 0 ? fmt->duration / (double)AV_TIME_BASE : 0;
	vidx = av_find_best_stream(fmt, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
	if (vidx < 0)
		die("no video in this file");
	aidx = av_find_best_stream(fmt, AVMEDIA_TYPE_AUDIO, -1, vidx, NULL, 0);
	for (unsigned i = 0; i < fmt->nb_streams; i++)
		if ((int)i != vidx && (int)i != aidx)
			fmt->streams[i]->discard = AVDISCARD_ALL;

	AVStream *vs = fmt->streams[vidx];
	const AVCodec *vc = avcodec_find_decoder(vs->codecpar->codec_id);
	if (!vc)
		die("can't decode %s video: convert it with vhsify", avcodec_get_name(vs->codecpar->codec_id));
	vdec = avcodec_alloc_context3(vc);
	avcodec_parameters_to_context(vdec, vs->codecpar);
	vdec->thread_count = 1;
	vdec->flags2 |= AV_CODEC_FLAG2_FAST;
	if (avcodec_open2(vdec, vc, NULL) < 0)
		die("can't decode this video: convert it with vhsify");
	if (vdec->width > 1280 || vdec->height > 720)
		die("%dx%d is too big for the RG Nano: convert it with vhsify", vdec->width, vdec->height);

	if (aidx >= 0) {
		AVStream *as = fmt->streams[aidx];
		const AVCodec *ac = avcodec_find_decoder(as->codecpar->codec_id);
		adec = ac ? avcodec_alloc_context3(ac) : NULL;
		if (adec) {
			avcodec_parameters_to_context(adec, as->codecpar);
			adec->thread_count = 1;
			if (avcodec_open2(adec, ac, NULL) < 0) {
				avcodec_free_context(&adec);
			}
		}
		if (!adec) {
			fprintf(stderr, "no sound: can't decode %s audio\n", avcodec_get_name(as->codecpar->codec_id));
			fmt->streams[aidx]->discard = AVDISCARD_ALL;
			aidx = -1;
		}
	}

	fb_open();
	if (opt_osd)
		osd_load(opt_osd);
	q_init(&vq);
	q_init(&aq);
	have_audio = audio_open();
	if (!have_audio && aidx >= 0) {
		fmt->streams[aidx]->discard = AVDISCARD_ALL;
		avcodec_free_context(&adec);
		aidx = -1;
	}

	if (opt_ss > 0.05)
		request_seek(opt_ss);
	else
		discard_before = 0;
	double stop_at = opt_t > 0 ? opt_ss + opt_t : -1;

	pthread_t dt, at, it;
	pthread_create(&dt, NULL, demux_main, NULL);
	if (have_audio)
		pthread_create(&at, NULL, audio_main, NULL);
	if (opt_dvr)
		pthread_create(&it, NULL, input_main, NULL);

	double tb = av_q2d(vs->time_base);
	double vstart = vs->start_time != AV_NOPTS_VALUE ? vs->start_time * tb : 0;
	AVFrame *fr = av_frame_alloc();
	int setup = 0, eof = 0, shown = 0, osd_was = 0, hud_was = 0, lagging = 0;
	double next_pts = 0, last_write = 0, wait_clock_since = 0, t_start = now_s();
	int rc = 0;

	while (!quit_flag) {
		/* the end: wait for the sound to finish too */
		if (eof) {
			int done = 1;
			if (have_audio) {
				pthread_mutex_lock(&st_mu);
				done = audio_done;
				pthread_mutex_unlock(&st_mu);
			}
			if (done || now_s() - wait_clock_since > 3)
				break;
			if (opt_dvr && now_s() < hud_until + 0.1) {
				/* keep the HUD responsive while the last second plays */
			}
			sleep_s(0.02);
			pthread_mutex_lock(&st_mu);
			int reopened = !demux_eof;
			pthread_mutex_unlock(&st_mu);
			if (reopened) /* a seek back from the very end */
				eof = 0;
			continue;
		}
		pthread_mutex_lock(&st_mu);
		int p = paused;
		pthread_mutex_unlock(&st_mu);
		if (p) {
			double c = master_clock();
			if (!isnan(c))
				last_pos = c;
			if (!hud_was || now_s() - last_write > 0.5) {
				int y = hud_draw(last_pos, duration, 1);
				fb_flush(y, scr.h);
				hud_was = 1;
			}
			if (now_s() - last_write > 1) {
				write_pos(last_pos);
				last_write = now_s();
			}
			sleep_s(0.03);
			continue;
		}
		AVPacket *pkt = q_get(&vq, 0.1);
		if (!pkt)
			continue;
		if (pkt == PKT_FLUSH) {
			avcodec_flush_buffers(vdec);
			pthread_mutex_lock(&st_mu);
			if (!have_audio)
				sclock_base = -1;
			pthread_mutex_unlock(&st_mu);
			continue;
		}
		if (pkt == PKT_EOF) {
			avcodec_send_packet(vdec, NULL);
		} else {
			avcodec_send_packet(vdec, pkt);
			av_packet_free(&pkt);
		}
		for (;;) {
			int r = avcodec_receive_frame(vdec, fr);
			if (r < 0)
				break;
			double pts = fr->best_effort_timestamp != AV_NOPTS_VALUE ? fr->best_effort_timestamp * tb - vstart : next_pts;
			double fdur = vs->avg_frame_rate.num > 0 ? av_q2d(av_inv_q(vs->avg_frame_rate)) : 1 / 30.0;
			next_pts = pts + fdur;
			pthread_mutex_lock(&st_mu);
			double disc = discard_before;
			pthread_mutex_unlock(&st_mu);
			if (pts < disc - 0.001) {
				av_frame_unref(fr);
				continue;
			}
			if (!setup || fr->width != src_w || fr->height != src_h || fr->format != src_pixfmt) {
				picture_setup(fr->width, fr->height, fr->sample_aspect_ratio, fr->format);
				setup = 1;
				shown = 0;
			}
			/* wait for our moment */
			double c = master_clock();
			if (isnan(c)) {
				if (have_audio) {
					/* sound is still starting: give it a moment */
					double t0 = now_s();
					while (isnan(c = master_clock()) && now_s() - t0 < 1 && !quit_flag)
						sleep_s(0.005);
				}
				if (isnan(c)) {
					pthread_mutex_lock(&st_mu);
					if (!have_audio)
						sclock_base = now_s() - pts;
					pthread_mutex_unlock(&st_mu);
					c = pts;
				}
			}
			int drop = 0;
			double diff = pts - c;
			if (diff < -0.12 && shown) {
				drop = 1; /* behind: catch up */
				if (!lagging && diff < -0.4) {
					vdec->skip_loop_filter = AVDISCARD_ALL;
					lagging = 1;
				}
			} else if (lagging && diff > -0.05) {
				vdec->skip_loop_filter = AVDISCARD_DEFAULT;
				lagging = 0;
			}
			while (diff > 0.002 && !quit_flag) {
				sleep_s(diff > 0.05 ? 0.05 : diff);
				pthread_mutex_lock(&st_mu);
				int changed = seek_target >= 0 || paused;
				pthread_mutex_unlock(&st_mu);
				if (changed)
					break;
				c = master_clock();
				if (isnan(c))
					break;
				diff = pts - c;
			}
			last_pos = pts;
			if (stop_at > 0 && pts >= stop_at) {
				quit_flag = 1;
				av_frame_unref(fr);
				break;
			}
			if (!drop) {
				picture_draw(fr);
				int osd_on = osd.n > 0 && pts >= osd_from && pts <= osd_to;
				int hud_on = opt_dvr && now_s() < hud_until;
				int y0 = scr.ry, y1 = scr.ry + scr.rh;
				if (osd_on) {
					osd_apply();
					if (osd.y0 < y0)
						y0 = osd.y0;
					if (osd.y1 > y1)
						y1 = osd.y1;
				}
				if (hud_on) {
					hud_draw(pts, duration, 0);
					y1 = scr.h;
				}
				/* Rabbit Ears may still be drawing snow while we start: own the
				 * whole screen (bars included) for the first moments */
				if (!shown || osd_on != osd_was || hud_on != hud_was || now_s() - t_start < 1.5) {
					if (!osd_on && osd_was) /* clear what it covered */
						for (int i = 0; i < osd.n; i++)
							if (osd.at[i] / scr.w < (unsigned)scr.ry || osd.at[i] / scr.w >= (unsigned)(scr.ry + scr.rh))
								scr.back[osd.at[i]] = 0;
					if (!hud_on && hud_was && !opt_fill)
						hud_rect(0, scr.h - 26, scr.w, 26, 0);
					y0 = 0, y1 = scr.h;
				}
				fb_flush(y0, y1);
				osd_was = osd_on;
				hud_was = hud_on;
				shown = 1;
			}
			av_frame_unref(fr);
			if (now_s() - last_write > 1) {
				write_pos(last_pos);
				last_write = now_s();
			}
		}
		if (pkt == PKT_EOF) {
			eof = 1;
			wait_clock_since = now_s();
		}
	}
	double c = master_clock();
	if (!isnan(c) && c > last_pos && (duration <= 0 || c <= duration))
		last_pos = c;
	write_pos(last_pos);
	if (pcm)
		snd_pcm_drop(pcm);
	fflush(stderr);
	_exit(rc);
}
