#!/usr/bin/env zsh
emulate -L zsh -o errexit -o nounset -o pipefail

readonly ROOT_DIR="${0:A:h}"
readonly STATE_DIR="$ROOT_DIR/.self_host"
readonly CONFIG_FILE="$STATE_DIR/config.env"
readonly RUN_API_SCRIPT="$STATE_DIR/run_api.zsh"
readonly LOG_DIR="$STATE_DIR/logs"
readonly API_LOG="$LOG_DIR/api.log"
readonly LOCK_CHECKSUM_FILE="$STATE_DIR/pnpm-lock.sha256"
readonly CADDYFILE="${CADDYFILE:-$HOME/Caddyfile}"
readonly DEFAULT_PUBLIC_URL='https://krimi.pinky.lilf.ir'
readonly DEFAULT_NODE_VERSION='20'
readonly DEFAULT_API_PORT='18082'
readonly DEFAULT_ROOM_TTL='168h'
readonly API_SESSION_NAME='krimi-api'
readonly CADDY_BEGIN='# BEGIN krimi self-host'
readonly CADDY_END='# END krimi self-host'

tmuxnew () {
	tmux kill-session -t "$1" &> /dev/null || true
	tmux new -d -s "$@"
}

usage() {
  cat <<USAGE
Usage: ./self_host.zsh [setup|redeploy|start|stop] [public_url]

setup     Stop any existing Krimi backend, persist config, build frontend/backend, update ~/Caddyfile, reload Caddy, and start Krimi.
redeploy  Rebuild from the current local checkout, refresh Caddy, and restart Krimi.
start     Start Krimi from existing artifacts and saved config.
stop      Stop the tmux-managed Krimi backend.

Default public_url: $DEFAULT_PUBLIC_URL
If public_url omits a scheme, https:// is assumed.
USAGE
}

die() {
  print -u2 -- "Error: $*"
  exit 1
}

note() {
  print -- "==> $*"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

copy_env_if_unset() {
  local target_name="$1"
  local source_name="$2"
  local source_value="${(P)source_name:-}"

  if [[ -z "${(P)target_name:-}" && -n "$source_value" ]]; then
    export "$target_name=$source_value"
  fi
}

load_proxy() {
  copy_env_if_unset http_proxy HTTP_PROXY
  copy_env_if_unset HTTP_PROXY http_proxy
  copy_env_if_unset https_proxy HTTPS_PROXY
  copy_env_if_unset HTTPS_PROXY https_proxy
  copy_env_if_unset all_proxy ALL_PROXY
  copy_env_if_unset ALL_PROXY all_proxy

  if [[ -z "${npm_config_proxy:-}" ]]; then
    local proxy_value="${https_proxy:-${HTTPS_PROXY:-${http_proxy:-${HTTP_PROXY:-}}}}"
    if [[ -n "$proxy_value" ]]; then
      export npm_config_proxy="$proxy_value"
    fi
  fi

  if [[ -z "${npm_config_https_proxy:-}" ]]; then
    local https_proxy_value="${https_proxy:-${HTTPS_PROXY:-${http_proxy:-${HTTP_PROXY:-}}}}"
    if [[ -n "$https_proxy_value" ]]; then
      export npm_config_https_proxy="$https_proxy_value"
    fi
  fi
}

normalize_public_url() {
  local raw_input="${1:-$DEFAULT_PUBLIC_URL}"
  python3 - "$raw_input" <<'PY'
import sys
from urllib.parse import urlparse

raw = sys.argv[1].strip()
if not raw:
    raise SystemExit('public_url must not be empty')
if '://' not in raw:
    raw = 'https://' + raw
parsed = urlparse(raw)
if parsed.scheme not in {'http', 'https'}:
    raise SystemExit('public_url must begin with http:// or https://')
if not parsed.netloc:
    raise SystemExit('public_url must include a hostname')
if parsed.path not in ('', '/'):
    raise SystemExit('public_url must not include a path')
if parsed.params or parsed.query or parsed.fragment:
    raise SystemExit('public_url must not include params, query, or fragment')
print(f'{parsed.scheme}://{parsed.netloc}')
PY
}

ensure_dirs() {
  mkdir -p "$STATE_DIR" "$LOG_DIR" "$STATE_DIR/bin" "$STATE_DIR/data"
}

ensure_prerequisites() {
  require_cmd tmux
  require_cmd caddy
  require_cmd go
  require_cmd cc
  require_cmd pnpm
  require_cmd python3
  require_cmd sha256sum
  require_cmd ss
  zsh -lc 'source ~/.shared.sh >/dev/null 2>&1 || true; type nvm-load >/dev/null 2>&1' || die 'nvm-load is required in zsh login shells'
}

bootstrap_node() {
  source ~/.shared.sh >/dev/null 2>&1 || true
  if ! command -v nvm-load >/dev/null 2>&1; then
    [[ -s "$HOME/.nvm_load" ]] && source "$HOME/.nvm_load"
    [[ -s "$HOME/.nvm/nvm.sh" ]] && source "$HOME/.nvm/nvm.sh"
  fi
  command -v nvm-load >/dev/null 2>&1 || die 'nvm-load is required in zsh shells'
  nvm-load >/dev/null 2>&1
  nvm use "$DEFAULT_NODE_VERSION" >/dev/null
}

load_config() {
  [[ -f "$CONFIG_FILE" ]] || die "Missing config file: $CONFIG_FILE. Run ./self_host.zsh setup first."
  source "$CONFIG_FILE"
  [[ -n "${PUBLIC_URL:-}" ]] || die "Saved config is missing PUBLIC_URL"
}

persist_config() {
  local public_url="$1"
  ensure_dirs
  cat > "$CONFIG_FILE" <<EOF_CONFIG
PUBLIC_URL='${public_url}'
NODE_VERSION='${DEFAULT_NODE_VERSION}'
API_PORT='${DEFAULT_API_PORT}'
ROOM_TTL='${DEFAULT_ROOM_TTL}'
EOF_CONFIG
}

resolve_configured_url() {
  if [[ -n "${1:-}" ]]; then
    normalize_public_url "$1" || die 'Invalid public URL'
  elif [[ -f "$CONFIG_FILE" ]]; then
    load_config
    normalize_public_url "$PUBLIC_URL" || die 'Saved public URL is invalid'
  else
    normalize_public_url "$DEFAULT_PUBLIC_URL" || die 'Default public URL is invalid'
  fi
}

current_lock_checksum() {
  sha256sum "$ROOT_DIR/pnpm-lock.yaml" | awk '{print $1}'
}

run_in_node_shell() {
  local command_string="$1"
  zsh -lc "source ~/.shared.sh >/dev/null 2>&1 || true; nvm-load >/dev/null 2>&1; nvm use ${(q)DEFAULT_NODE_VERSION} >/dev/null; cd ${(q)ROOT_DIR}; ${command_string}"
}

install_dependencies_if_needed() {
  local new_checksum existing_checksum=''
  new_checksum="$(current_lock_checksum)"
  if [[ -f "$LOCK_CHECKSUM_FILE" ]]; then
    existing_checksum="$(<"$LOCK_CHECKSUM_FILE")"
  fi

  if [[ ! -d "$ROOT_DIR/node_modules" || "$new_checksum" != "$existing_checksum" ]]; then
    note 'Installing pnpm dependencies...'
    load_proxy
    run_in_node_shell 'pnpm install --frozen-lockfile --prefer-offline'
    print -- "$new_checksum" > "$LOCK_CHECKSUM_FILE"
  else
    note 'pnpm dependencies already match pnpm-lock.yaml; skipping install.'
  fi
}

build_frontend() {
  load_config
  note 'Building frontend bundle...'
  load_proxy
  run_in_node_shell "export VUE_APP_PUBLIC_ORIGIN=${(q)PUBLIC_URL}; pnpm build"
}

build_backend() {
  note 'Building Go backend...'
  load_proxy
  (cd "$ROOT_DIR/server" && go build -o "$STATE_DIR/bin/krimi-server" ./cmd/krimi-server)
}

write_run_api_script() {
  load_config
  cat > "$RUN_API_SCRIPT" <<EOF_RUN
#!/usr/bin/env zsh
emulate -L zsh -o errexit -o nounset -o pipefail

readonly ROOT_DIR='${ROOT_DIR}'
readonly STATE_DIR='${STATE_DIR}'
readonly CONFIG_FILE='${CONFIG_FILE}'
readonly API_LOG='${API_LOG}'

source "\$CONFIG_FILE"
mkdir -p "\${STATE_DIR}/data" "\${API_LOG:h}"
cd "\$ROOT_DIR"

export KRIMI_ADDR="127.0.0.1:\$API_PORT"
export KRIMI_DATA_DIR="\${STATE_DIR}/data"
export KRIMI_DB_PATH="\${STATE_DIR}/data/krimi.sqlite"
export KRIMI_ROOM_TTL="\$ROOM_TTL"

"\${STATE_DIR}/bin/krimi-server" 2>&1 | tee -a "\$API_LOG"
EOF_RUN
  chmod +x "$RUN_API_SCRIPT"
}

port_is_busy() {
  local port="$1"
  ss -ltn "( sport = :${port} )" | tail -n +2 | grep -q LISTEN
}

ensure_api_port_available() {
  load_config
  if port_is_busy "$API_PORT"; then
    die "Port ${API_PORT} is already in use; stop the conflicting process and retry."
  fi
}

render_caddy_block() {
  load_config
  python3 - "$PUBLIC_URL" "$API_PORT" "$ROOT_DIR/dist" <<'PY'
import sys
from urllib.parse import urlparse

public_url, api_port, dist_dir = sys.argv[1:4]
parsed = urlparse(public_url)
host = parsed.netloc
common = f"""    encode zstd gzip\n\n    @krimi_backend {{\n        path /api* /ws* /healthz\n    }}\n\n    handle @krimi_backend {{\n        reverse_proxy 127.0.0.1:{api_port}\n    }}\n\n    root * {dist_dir}\n    try_files {{path}} /index.html\n    file_server\n"""
blocks = []
if parsed.scheme == 'https':
    blocks.append(f"https://{host} {{\n    tls internal\n{common}}}")
    blocks.append(f"http://{host} {{\n{common}}}")
else:
    blocks.append(f"http://{host} {{\n{common}}}")
print('\n\n'.join(blocks))
PY
}

update_caddyfile() {
  [[ -f "$CADDYFILE" ]] || touch "$CADDYFILE"
  local candidate="$STATE_DIR/Caddyfile.candidate"
  local block_contents
  block_contents="$(render_caddy_block)"

  TARGET_CADDYFILE="$CADDYFILE" BLOCK_BEGIN="$CADDY_BEGIN" BLOCK_END="$CADDY_END" BLOCK_CONTENTS="$block_contents" OUTPUT_PATH="$candidate" python3 - <<'PY'
import os
import pathlib
import re

caddyfile = pathlib.Path(os.environ['TARGET_CADDYFILE'])
text = caddyfile.read_text() if caddyfile.exists() else ''
begin = os.environ['BLOCK_BEGIN']
end = os.environ['BLOCK_END']
block = begin + '\n' + os.environ['BLOCK_CONTENTS'].rstrip() + '\n' + end + '\n'
pattern = re.compile(re.escape(begin) + r'.*?' + re.escape(end) + r'\n?', re.S)
if pattern.search(text):
    updated = pattern.sub(block, text)
else:
    updated = text.rstrip() + ('\n\n' if text.strip() else '') + block
pathlib.Path(os.environ['OUTPUT_PATH']).write_text(updated)
PY

  caddy validate --config "$candidate" >/dev/null
  cp "$candidate" "$CADDYFILE"
  caddy reload --config "$CADDYFILE" >/dev/null
}

start_api() {
  load_config
  ensure_api_port_available
  [[ -x "$RUN_API_SCRIPT" ]] || die "Missing run script: $RUN_API_SCRIPT"
  [[ -x "$STATE_DIR/bin/krimi-server" ]] || die "Missing backend binary: $STATE_DIR/bin/krimi-server"
  note 'Starting tmux-managed Krimi backend...'
  tmuxnew "$API_SESSION_NAME" "$RUN_API_SCRIPT"
}

stop_api() {
  tmux kill-session -t "$API_SESSION_NAME" &> /dev/null || true
}

setup_command() {
  local public_url
  public_url="$(resolve_configured_url "${1:-}")"
  stop_api
  persist_config "$public_url"
  install_dependencies_if_needed
  build_frontend
  build_backend
  write_run_api_script
  update_caddyfile
  start_api
  note "Krimi is configured for $public_url"
}

redeploy_command() {
  local public_url
  public_url="$(resolve_configured_url "${1:-}")"
  stop_api
  persist_config "$public_url"
  install_dependencies_if_needed
  build_frontend
  build_backend
  write_run_api_script
  update_caddyfile
  start_api
  note "Krimi redeployed for $public_url"
}

start_command() {
  if [[ -n "${1:-}" ]]; then
    persist_config "$(resolve_configured_url "$1")"
  else
    load_config
  fi
  write_run_api_script
  update_caddyfile
  start_api
  note "Krimi started for ${PUBLIC_URL}"
}

main() {
  local command="${1:-}"
  local public_url="${2:-}"
  ensure_dirs

  case "$command" in
    setup)
      ensure_prerequisites
      bootstrap_node
      setup_command "$public_url"
      ;;
    redeploy)
      ensure_prerequisites
      bootstrap_node
      redeploy_command "$public_url"
      ;;
    start)
      require_cmd tmux
      require_cmd caddy
      require_cmd python3
      start_command "$public_url"
      ;;
    stop)
      require_cmd tmux
      stop_api
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
