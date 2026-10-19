#!/usr/bin/env bash

inspection_resolve_component_catalog() {
  local script_path="$1" script_dir candidate version releases_root
  script_dir="$(dirname "$script_path")"
  if [[ -n "${INSPECTION_COMPONENT_CATALOG:-}" ]]; then
    candidate="$INSPECTION_COMPONENT_CATALOG"
  elif [[ -e "$script_dir/components.json" ]]; then
    candidate="$script_dir/components.json"
  elif [[ -e "$script_dir/../components.json" ]]; then
    candidate="$script_dir/../components.json"
  else
    version="$(basename "$script_dir")"
    releases_root="${INSPECTION_RELEASES_ROOT:-/opt/inspection/releases}"
    if [[ "$version" =~ ^[a-f0-9]{40}$ ]]; then
      candidate="$releases_root/$version/deploy/oci/components.json"
    else
      candidate=""
    fi
  fi
  [[ -n "$candidate" && -f "$candidate" ]] || {
    echo "Component catalog was not found for host script: $script_path" >&2
    return 1
  }
  python3 - "$candidate" <<'PY'
import json, sys
try:
    components = json.load(open(sys.argv[1], encoding="utf-8"))["components"]
except (OSError, ValueError, KeyError, TypeError) as exc:
    raise SystemExit(f"invalid or missing component catalog: {exc}")
if not isinstance(components, dict) or not components or any(
    not isinstance(name, str)
    or not name
    or not isinstance(item, dict)
    or not isinstance(item.get("image"), str)
    or not item["image"]
    or not (item.get("service") is None or isinstance(item.get("service"), str) and item["service"])
    for name, item in components.items()
):
    raise SystemExit("component catalog must define image and service for every component")
PY
  export INSPECTION_RESOLVED_COMPONENT_CATALOG="$candidate"
}
