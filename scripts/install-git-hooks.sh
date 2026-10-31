#!/usr/bin/env sh
set -eu

workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

git -C "$workspace" config core.hooksPath .githooks

printf '%s\n' 'Hooks Git ativados para este clone.'
printf '%s\n' 'O pre-push validará artefatos gerados e testes unitários.'
