#!/usr/bin/env bash
set -Eeuo pipefail

workspace="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/bin"
cat >"$tmp_dir/bin/node" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$tmp_dir/bin/npm" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "run" && "${2:-}" == "dev" ]]; then
  printf '%s\n' "${NEXT_PUBLIC_INSPECTION_API_URL:-}"
  printf '%s\n' "${INSPECTION_ALLOWED_ORIGINS:-}"
fi
EOF
chmod +x "$tmp_dir/bin/node" "$tmp_dir/bin/npm"

sed \
  -e 's/^INSPECTION_API_PORT=.*/INSPECTION_API_PORT=8180/' \
  -e 's/^INSPECTION_ADMIN_PORT=.*/INSPECTION_ADMIN_PORT=3100/' \
  -e 's/^INSPECTION_ALLOWED_ORIGINS=.*/INSPECTION_ALLOWED_ORIGINS=http:\/\/localhost:3000,http:\/\/localhost:3002,http:\/\/localhost:3003/' \
  "$workspace/.env.example" >"$tmp_dir/env"
printf '%s\n' 'NEXT_PUBLIC_INSPECTION_API_URL=http://localhost:8080/graphql' >>"$tmp_dir/env"

actual="$(PATH="$tmp_dir/bin:$PATH" INSPECTION_ENV_FILE="$tmp_dir/env" "$workspace/scripts/dev.sh" admin)"
expected="http://localhost:8180/graphql"
[[ "$actual" == *"$expected"* ]] || {
  printf 'expected web endpoint %s, got %s\n' "$expected" "$actual" >&2
  exit 1
}
expected_origin="http://localhost:3100"
[[ "$actual" == *"$expected_origin"* ]] || {
  printf 'expected admin port %s, got %s\n' "$expected_origin" "$actual" >&2
  exit 1
}
