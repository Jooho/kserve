#!/usr/bin/env python3
"""
Kustomize to Helm Chart Converter

This script converts Kubernetes manifests (managed by kustomize) into Helm charts
based on a mapping configuration file.

Usage:
    python convert.py --mapping helm-mapping-kserve.yaml --output charts/kserve
    python convert.py --mapping helm-mapping-llmisvc.yaml --output charts/llmisvc

Arguments:
    --mapping: Path to the helm mapping YAML file
    --output: Output directory for the generated Helm chart (will not overwrite if exists)
    --force: Force overwrite if output directory exists
"""

import argparse
import sys
import os
from pathlib import Path

from helm_converter.manifest_reader import ManifestReader
from helm_converter.chart_generator import ChartGenerator
from helm_converter.values_generator import ValuesGenerator


def main():
    parser = argparse.ArgumentParser(
        description="Convert Kustomize manifests to Helm charts using mapping configuration"
    )
    parser.add_argument(
        "--mapping",
        required=True,
        help="Path to the helm mapping YAML file (e.g., helm-mapping-kserve.yaml)",
    )
    parser.add_argument(
        "--output",
        required=True,
        help="Output directory for the generated Helm chart (e.g., charts/kserve)",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Force overwrite if output directory exists",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Dry run mode - show what would be generated without creating files",
    )

    args = parser.parse_args()

    # Resolve paths
    mapping_file = Path(args.mapping).resolve()
    output_dir = Path(args.output).resolve()

    # Get the repository root (assume we're in hack/setup/scripts/helm-converter/)
    repo_root = Path(__file__).resolve().parent.parent.parent.parent.parent

    # Validate mapping file exists
    if not mapping_file.exists():
        print(f"Error: Mapping file not found: {mapping_file}")
        sys.exit(1)

    # Check if output directory exists
    if output_dir.exists() and not args.force and not args.dry_run:
        print(f"Error: Output directory already exists: {output_dir}")
        print("Use --force to overwrite or choose a different output directory")
        sys.exit(1)

    print(f"Kustomize to Helm Chart Converter")
    print(f"=" * 60)
    print(f"Mapping file: {mapping_file}")
    print(f"Output directory: {output_dir}")
    print(f"Repository root: {repo_root}")
    print(f"Dry run: {args.dry_run}")
    print(f"=" * 60)
    print()

    try:
        # Step 1: Read the mapping file
        print("[1/4] Reading mapping configuration...")
        reader = ManifestReader(mapping_file, repo_root)
        mapping = reader.load_mapping()
        print(f"  ✓ Loaded mapping for chart: {mapping['metadata']['name']}")
        print()

        # Step 2: Read and parse manifests
        print("[2/4] Reading Kubernetes manifests...")
        manifests = reader.read_manifests(mapping)
        print(f"  ✓ Read {len(manifests)} manifest files")
        print()

        # Step 3: Generate Helm templates
        print("[3/4] Generating Helm templates...")
        chart_gen = ChartGenerator(mapping, manifests, output_dir, repo_root)

        if args.dry_run:
            print("  (Dry run mode - not creating files)")
            chart_gen.show_plan()
        else:
            chart_gen.generate()
            print(f"  ✓ Generated Helm templates in {output_dir}/templates/")
        print()

        # Step 4: Generate values.yaml
        print("[4/4] Generating values.yaml...")
        values_gen = ValuesGenerator(mapping, manifests, output_dir)

        if args.dry_run:
            print("  (Dry run mode - not creating files)")
            values_gen.show_plan()
        else:
            values_gen.generate()
            print(f"  ✓ Generated values.yaml")
        print()

        print("=" * 60)
        print("✓ Conversion completed successfully!")
        print(f"  Chart location: {output_dir}")
        if not args.dry_run:
            print(f"\nNext steps:")
            print(f"  1. Review generated templates: {output_dir}/templates/")
            print(f"  2. Review values.yaml: {output_dir}/values.yaml")
            print(f"  3. Test the chart: helm template {mapping['metadata']['name']} {output_dir}")
            print(f"  4. Install the chart: helm install {mapping['metadata']['name']} {output_dir}")

    except Exception as e:
        print(f"\n✗ Error during conversion: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)


if __name__ == "__main__":
    main()
