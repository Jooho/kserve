#!/usr/bin/env bash
set -euo pipefail

for chart in charts/*; do
  if [[ -f "$chart/Chart.yaml" ]]; then
    echo "→ Linting $(basename "$chart")"
    helm lint "$chart" --quiet
  fi
done

echo "✓ All charts passed lint"
