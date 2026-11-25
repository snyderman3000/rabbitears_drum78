#!/bin/sh
# Builds tvplay, the RG Nano's video player, as one static ARMv7 binary:
# FFmpeg (decode only: MP4 with H.264/MPEG-4 video, AAC/MP3 audio) and
# alsa-lib, cross-compiled from source.
#
#   sudo apt install gcc-arm-linux-gnueabihf autoconf automake libtool pkg-config
#   tvplay/build.sh            # → tvplay/out/tvplay
#
# Sources are fetched from GitHub into tvplay/out/src the first time.
set -e
cd "$(dirname "$0")"
HERE=$PWD
OUT=$HERE/out
PREFIX=$OUT/arm
CROSS=${CROSS:-arm-linux-gnueabihf-}
CPU="-mcpu=cortex-a7 -mfpu=neon-vfpv4 -mfloat-abi=hard"
FFMPEG_TAG=n6.1.2
ALSA_TAG=v1.2.10
JOBS=$(nproc 2>/dev/null || echo 4)
mkdir -p "$OUT/src" "$PREFIX"

if [ ! -f "$PREFIX/lib/libasound.a" ]; then
	[ -d "$OUT/src/alsa-lib" ] || git clone -q --depth 1 -b $ALSA_TAG https://github.com/alsa-project/alsa-lib "$OUT/src/alsa-lib"
	(
		cd "$OUT/src/alsa-lib"
		autoreconf -fi >/dev/null 2>&1
		CFLAGS="-O2 $CPU" ./configure --host=${CROSS%-} --prefix="$PREFIX" --enable-static --disable-shared \
			--disable-python --disable-ucm --disable-topology --disable-alisp --disable-rawmidi --disable-hwdep \
			--disable-seq --with-libdl=no --with-configdir=/usr/share/alsa --with-plugindir=/usr/lib/alsa-lib >/dev/null
		make -j"$JOBS" >/dev/null
		make install >/dev/null
	)
fi

if [ ! -f "$PREFIX/lib/libavcodec.a" ]; then
	[ -d "$OUT/src/ffmpeg" ] || git clone -q --depth 1 -b $FFMPEG_TAG https://github.com/FFmpeg/FFmpeg "$OUT/src/ffmpeg"
	(
		cd "$OUT/src/ffmpeg"
		./configure --prefix="$PREFIX" --arch=arm --cpu=cortex-a7 --target-os=linux --cross-prefix=$CROSS \
			--enable-cross-compile --extra-cflags="-mfpu=neon-vfpv4 -mfloat-abi=hard -O2" --enable-neon \
			--enable-static --disable-shared --disable-programs --disable-doc --disable-debug \
			--disable-everything --disable-autodetect --disable-network --disable-avdevice --disable-avfilter \
			--disable-swscale --disable-postproc --enable-avformat --enable-avcodec --enable-swresample \
			--enable-demuxer=mov --enable-decoder=h264,aac,aac_fixed,mp3,mp3float,mpeg4,mjpeg \
			--enable-parser=h264,aac,mpeg4video,mpegaudio --enable-protocol=file >/dev/null
		make -j"$JOBS" >/dev/null
		make install >/dev/null
	)
fi

${CROSS}gcc -O2 $CPU -std=gnu11 -Wall -Wno-deprecated-declarations -I"$PREFIX/include" \
	-o "$OUT/tvplay" tvplay.c -static -L"$PREFIX/lib" \
	-lavformat -lavcodec -lswresample -lavutil -lasound -lpthread -lm
${CROSS}strip "$OUT/tvplay"
ls -l "$OUT/tvplay"
