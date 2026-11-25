#!/bin/sh
# Rabbit Ears — a 90s TV set (RG Nano / DrUm78 FunKey-OS).
# The OPK is mounted read-only; settings, logs and DVR marks live in
# /mnt/FunKey/RabbitEars. exec, so the power button's SIGUSR1 reaches the
# app itself (it cancels the shutdown and shows its own power menu).
cd "$(dirname "$0")" || exit 1
mkdir -p /mnt/FunKey/RabbitEars 2>/dev/null
exec ./rabbitears 2>>/mnt/FunKey/RabbitEars/stderr.log
