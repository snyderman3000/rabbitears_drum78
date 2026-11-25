# Third-party notices

Rabbit Ears itself is MIT-licensed (see [LICENSE](LICENSE)). The RG Nano package bundles the following, each under its own license; full texts are in [`package/opk/licenses`](package/opk/licenses):

| Component | License |
|---|---|
| VT323, Share Tech Mono and Orbitron fonts | SIL Open Font License 1.1 |
| golang.org/x/image | BSD-3-Clause |
| FFmpeg 6.1.2 (libavformat, libavcodec, libavutil, libswresample), statically linked into `tvplay` | LGPL-2.1-or-later |
| alsa-lib 1.2.10, statically linked into `tvplay` | LGPL-2.1-or-later |

`tvplay` is built entirely from public sources by [`tvplay/build.sh`](tvplay/build.sh) (FFmpeg tag `n6.1.2`, alsa-lib tag `v1.2.10`, configured as shown there), so it can be rebuilt or relinked against modified versions of those libraries. vhsify calls the user's own ffmpeg.
