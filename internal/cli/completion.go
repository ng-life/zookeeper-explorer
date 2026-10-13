package cli

// Shell wrappers pass word arrays directly, preserving spaces without eval.
const zshCompletion = `# Load after compinit.
_zookeeper_explorer() {
  local -a args lines candidates
  args=("${(@Q)words[2,CURRENT]}")
  lines=("${(@f)$("${words[1]}" __complete "${args[@]}" 2>/dev/null)}")
  local directive="${lines[-1]#:}"
  candidates=("${lines[@]:0:$(( ${#lines}-1 ))}")
  if [[ "$PREFIX" == --config=* || "$PREFIX" == -c=* ]]; then
    compset -P '*='; _files; return
  fi
  if (( directive == 0 )); then _files; return; fi
  [[ "$PREFIX" == --*=* ]] && compset -P '*='
  if (( directive & 2 )); then compadd -S '' -- "${candidates[@]}"
  else compadd -- "${candidates[@]}"; fi
}
compdef _zookeeper_explorer zookeeper-explorer
`
const bashCompletion = `# Compatible with Bash 3; no bash-completion dependency.
_zookeeper_explorer_unquote() {
  local input="$1" state='' char next i
  REPLY=''
  for ((i=0;i<${#input};i++)); do
    char="${input:i:1}"
    if [[ "$char" == "'" && "$state" != double ]]; then
      if [[ "$state" == single ]]; then state=''; else state=single; fi
    elif [[ "$char" == '"' && "$state" != single ]]; then
      if [[ "$state" == double ]]; then state=''; else state=double; fi
    elif [[ "$char" == '\' && "$state" != single ]]; then
      next="${input:i+1:1}"
      if [[ "$state" != double || "$next" == '$' || "$next" == '"' || "$next" == '\' || "$next" == $'\x60' ]]; then
        ((i++)); REPLY="$REPLY$next"
      else REPLY="$REPLY$char"; fi
    else REPLY="$REPLY$char"; fi
  done
}
_zookeeper_explorer() {
  local -a args lines
  local i word cur directive line
  args=()
  for ((i=1;i<=COMP_CWORD;i++)); do
    _zookeeper_explorer_unquote "${COMP_WORDS[i]}"
    word="$REPLY"
    if [[ "$word" == = && ${#args[@]} -gt 0 ]]; then
      args[$((${#args[@]}-1))]="${args[$((${#args[@]}-1))]}="
    elif [[ ${#args[@]} -gt 0 && "${args[$((${#args[@]}-1))]}" == --*= ]]; then
      args[$((${#args[@]}-1))]="${args[$((${#args[@]}-1))]}$word"
    else args[${#args[@]}]="$word"; fi
  done
  _zookeeper_explorer_unquote "${COMP_WORDS[COMP_CWORD]}"
  cur="$REPLY"
  [[ "$cur" == = ]] && cur=''
  [[ "$cur" == --*=* ]] && cur="${cur#*=}"
  lines=()
  while IFS= read -r line; do lines[${#lines[@]}]="$line"; done < <("${COMP_WORDS[0]}" __complete "${args[@]}" 2>/dev/null)
  directive="${lines[$((${#lines[@]}-1))]#:}"
  COMPREPLY=()
  if [[ "$directive" == 0 ]]; then
    while IFS= read -r line; do COMPREPLY[${#COMPREPLY[@]}]="$(printf '%q' "$line")"; done < <(compgen -f -- "$cur")
    type compopt >/dev/null 2>&1 && compopt -o filenames
    return
  fi
  for ((i=0;i<${#lines[@]}-1;i++)); do
    line="${lines[i]}"
    [[ "$line" == "$cur"* ]] || continue
    line="$(printf '%q' "$line")"
    if (( !(directive & 2) )); then line="$line "; fi
    COMPREPLY[${#COMPREPLY[@]}]="$line"
  done
}
complete -o nospace -F _zookeeper_explorer zookeeper-explorer
`
