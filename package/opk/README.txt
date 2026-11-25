RABBIT EARS — a 90s TV set for the RG Nano (DrUm78 FunKey-OS)
==============================================================
Built by Claude, Anthropic's AI model, for snyderman3000.
Source, the vhsify converter and updates: https://github.com/snyderman3000/rabbitears_drum78

Put videos on the SD card and they become TV channels. Every channel is
always "on the air": tune in and you land mid-show, based on the clock,
like real TV. Flip away and back and the show has moved on.

INSTALL
  Copy the "Applications" and "Media" folders from this zip to the root of
  your SD card (merge them with what's there). Open Rabbit Ears from
  Applications. Its own video player (tvplay) is inside the OPK.

ADDING CHANNELS  (SD card: Media/TV)
  A video file          → a channel that loops that video
  A folder of videos    → a channel that airs them, then repeats
  Show folders inside   → each one is a show; the channel rotates between
  a channel folder        them one episode at a time (Doug ep 1, Rugrats
                          ep 1, Doug ep 2 ...). Season folders inside a
                          show stay in order ("Ep 2" before "Ep 10").
  Start a name with a number to pick the channel:  "07 Cartoons",
  "12 - Late Movie.mp4". Everything else is numbered from channel 2 up.

  Example:
    Media/TV/02 Cartoons/Doug/Season 1/Doug S01E01.mp4
    Media/TV/02 Cartoons/Rugrats/Season 1/Rugrats S01E01.mp4
    Media/TV/05 Late Movie.mp4
    Media/TV/Commercials/Crystal Pepsi.mp4
    Media/TV/Commercials/02 Cartoons/Gushers.mp4

  Show order (SETUP menu): ROTATE SHOWS, IN ORDER (every episode, folder by
  folder) or SHUFFLE. To set one channel differently, put a file called
  order.txt in its folder containing: rotate, in order or shuffle.

  Convert videos with vhsify first (on your PC or Pi). It makes them small
  (about 180 MB an hour), quick to tune in to, and look like a VHS tape.
  The Nano has a single core: unconverted MP4s may stutter (anything over
  1280x720 is refused), and only MP4 files (H.264 / MPEG-4 video, AAC /
  MP3 sound) play.

COMMERCIALS  (SD card: Media/TV/Commercials)
  Put old commercials (MP4, up to 5 minutes each) in Media/TV/Commercials.
  In live TV they play before each show and in breaks during it. A folder
  inside Commercials named after a channel ("02 Cartoons" or just
  "Cartoons") holds commercials for that channel only; every other
  commercial is shared by the channels without their own.
  In SETUP: turn commercials on or off, choose how often breaks come
  (every 6 to 15 minutes, or only between shows) and how many commercials
  play in each break. Breaks are part of the schedule, so the guide's times
  stay right.

DVR MODE  (SETUP > MODE > DVR)
  Instead of live channels, pick any channel, show, season and episode
  and watch it from the start. Rabbit Ears remembers where you stopped and
  picks up from there, plays the next episode of the same show when one
  ends, and marks what you've watched. No commercials in DVR mode.

CONTROLS — LIVE TV
  Up / Down, L / R, Left / Right   change channel
  B                                previous channel (recall)
  A or Y                           display: channel, show, time left
  X or Select                      channel guide
  Start                            SETUP menu
  Power button                     power menu: resume / turn TV off /
                                   shut down
  Volume and brightness: the system's own FN combos.

CONTROLS — DVR LIST
  A          open a folder / play (resumes where you stopped)
  Y          play from the beginning
  X          mark watched / unwatched (a whole folder at once)
  B          back up a folder
  Select     back to live TV
  Start      SETUP menu

CONTROLS — WATCHING IN DVR MODE
  Left / Right  -/+10 seconds     Down / Up  -/+1 minute
  L / R         -/+10 minutes     A          pause
  B or Select   stop and go back to the list

SETTINGS  (FunKey/RabbitEars/config.json on the SD card, created on first
run; the SETUP menu changes most of them)
  mode            "live" or "dvr"
  picture         "4:3" (black bars) or "zoom" (fill the square screen)
  show_order      "rotate", "in order" or "shuffle"
  commercials     true or false
  ads_every_min   minutes between breaks inside a show (0 = between shows)
  ads_per_break   commercials in each break
  tv_folder       where your channels live (default /mnt/Media/TV)
  first_channel   number of the first channel (default 2)
  static_ms       how long the snow shows when you change channel
  osd_seconds     how long the channel number stays on screen
  power_on_last   turn on to the channel you were watching

LOGS: FunKey/RabbitEars/rabbitears.log and stderr.log on the SD card
      (the player's own: /tmp/rabbitears/tvplay.log)

Fonts: VT323, Share Tech Mono, Orbitron (SIL Open Font License).
Video playback: tvplay, built with FFmpeg and alsa-lib (LGPL-2.1, see
licenses/; source and build script in the GitHub repository).
