#!/usr/bin/env bash
set -euo pipefail

version="v0.3.0"
repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/moco-crd-ref-docs-templates.XXXXXX")
trap 'rm -rf -- "$temp_dir"' EXIT

templates=(gv_list.tpl gv_details.tpl type.tpl type_members.tpl)
for template in "${templates[@]}"; do
    curl --fail --silent --show-error --location \
        "https://raw.githubusercontent.com/elastic/crd-ref-docs/$version/templates/markdown/$template" \
        --output "$temp_dir/$template"
done

patch --directory="$temp_dir" --strip=1 --batch --forward --fuzz=0 \
    < "$repo_root/crd-ref-docs-template/crd-ref-docs-templates.patch"

mkdir -p "$repo_root/crd-ref-docs-templates"
for template in "${templates[@]}"; do
    cp -- "$temp_dir/$template" "$repo_root/crd-ref-docs-templates/$template"
done

printf 'Updated Markdown templates from elastic/crd-ref-docs %s\n' "$version"
