#!/usr/bin/env bash
# Hook input is shell text, not code to execute. Preserve quoted arguments
# and command boundaries without eval, including commands after abort/quit.
hook_each_command() {
  local text="$1" callback="$2" char quote="" word="" started=0 escaped=0 i
  local words=()
  HOOK_CWD="$REPO_ROOT"
  for ((i=0; i<${#text}; i++)); do
    char="${text:i:1}"
    if [ "$escaped" -eq 1 ]; then
      word+="$char"; escaped=0; continue
    fi
    if [ "$quote" = "'" ]; then
      if [ "$char" = "'" ]; then quote=""; else word+="$char"; fi
      continue
    fi
    if [ "$char" = '\' ]; then escaped=1; started=1; continue; fi
    if [ "$quote" = '"' ]; then
      if [ "$char" = '"' ]; then quote=""; else word+="$char"; fi
      continue
    fi
    case "$char" in
      "'"|'"') quote="$char"; started=1 ;;
      ' '|$'\t')
        if [ "$started" -eq 1 ]; then words+=("$word"); word=""; started=0; fi ;;
      ';'|'&'|'|'|$'\n'|'('|')')
        if [ "$started" -eq 1 ]; then words+=("$word"); word=""; started=0; fi
        if [ "${#words[@]}" -gt 0 ]; then hook_dispatch "$callback" "${words[@]}" || return 2; fi
        words=() ;;
      *) word+="$char"; started=1 ;;
    esac
  done
  [ -z "$quote" ] && [ "$escaped" -eq 0 ] || return 2
  if [ "$started" -eq 1 ]; then words+=("$word"); fi
  if [ "${#words[@]}" -gt 0 ]; then hook_dispatch "$callback" "${words[@]}" || return 2; fi
  return 0
}

hook_dispatch() {
  local callback="$1"
  shift
  if [ "$1" = cd ]; then
    HOOK_CWD=$(cd "${HOOK_CWD:-/}" && cd "${2:-$HOME}" && pwd) || HOOK_CWD=""
    return 0
  fi
  "$callback" "$@"
}

hook_normalize_command() {
  HOOK_WORDS=("$@")
  while [ "${#HOOK_WORDS[@]}" -gt 0 ]; do
    case "${HOOK_WORDS[0]}" in
      *=*|command|env|sudo|if|then|do|'!') HOOK_WORDS=("${HOOK_WORDS[@]:1}") ;;
      *) break ;;
    esac
  done
}

hook_git_command() {
  hook_normalize_command "$@"
  [ "${#HOOK_WORDS[@]}" -gt 0 ] && [ "${HOOK_WORDS[0]##*/}" = git ] || return 1
  HOOK_GIT_OPTIONS=(-C "${HOOK_CWD:-/nonexistent-hook-directory}")
  HOOK_SUBCOMMAND=""
  HOOK_ARGS=()
  local i token
  for ((i=1; i<${#HOOK_WORDS[@]}; i++)); do
    token="${HOOK_WORDS[i]}"
    case "$token" in
      -C|-c|--git-dir|--work-tree|--namespace|--config-env)
        [ "$((i+1))" -lt "${#HOOK_WORDS[@]}" ] || return 2
        HOOK_GIT_OPTIONS+=("$token" "${HOOK_WORDS[i+1]}")
        i=$((i+1)) ;;
      -*) HOOK_GIT_OPTIONS+=("$token") ;;
      *)
        HOOK_SUBCOMMAND="$token"
        HOOK_ARGS=("${HOOK_WORDS[@]:i+1}")
        return 0 ;;
    esac
  done
  return 1
}
