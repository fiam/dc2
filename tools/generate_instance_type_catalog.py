#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["boto3>=1.35.0"]
# ///

from __future__ import annotations

import argparse
import json
import sys
from datetime import UTC, date, datetime
from decimal import Decimal
from pathlib import Path
from typing import Any

import boto3
from botocore.exceptions import BotoCoreError, ClientError


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate EC2 instance type catalog from live AWS APIs",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=Path("pkg/dc2/instancetype/data/instance_types.json"),
        help="Output JSON file",
    )
    parser.add_argument(
        "--region",
        default="us-east-1",
        help="Source region for DescribeInstanceTypes (default: us-east-1)",
    )
    parser.add_argument(
        "--profile",
        default="",
        help="AWS profile name to use",
    )
    parser.add_argument(
        "--allow-empty",
        action="store_true",
        help="Allow writing an empty catalog when AWS cannot be queried",
    )
    return parser.parse_args()


def json_safe(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: json_safe(item) for key, item in value.items()}
    if isinstance(value, list):
        return [json_safe(item) for item in value]
    if isinstance(value, datetime):
        return value.astimezone(UTC).isoformat().replace("+00:00", "Z")
    if isinstance(value, date):
        return value.isoformat()
    if isinstance(value, Decimal):
        if value % 1 == 0:
            return int(value)
        return float(value)
    return value


def main() -> int:
    args = parse_args()

    session_kwargs: dict[str, Any] = {}
    if args.profile:
        session_kwargs["profile_name"] = args.profile

    session = boto3.Session(**session_kwargs)
    ec2 = session.client("ec2", region_name=args.region)

    instance_types: dict[str, dict[str, Any]] = {}
    try:
        paginator = ec2.get_paginator("describe_instance_types")
        for page in paginator.paginate():
            for raw_item in page.get("InstanceTypes", []):
                item = json_safe(raw_item)
                instance_type = item.get("InstanceType")
                if not isinstance(instance_type, str) or not instance_type:
                    continue
                instance_types[instance_type] = item
    except (BotoCoreError, ClientError) as exc:
        if not args.allow_empty:
            print(f"failed to query {args.region}: {exc}", file=sys.stderr)
            return 1
        print(f"failed to query {args.region}: {exc}", file=sys.stderr)
        print("continuing with an empty catalog because --allow-empty was set", file=sys.stderr)

    if not instance_types and not args.allow_empty:
        print("no instance type data was collected", file=sys.stderr)
        return 1

    sorted_instance_types = {
        instance_type: instance_types[instance_type]
        for instance_type in sorted(instance_types)
    }

    payload = {
        "generated_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        "query": {
            "source_region": args.region,
        },
        "stats": {
            "instance_type_count": len(sorted_instance_types),
            "offering_count": 0,
        },
        "instance_types": sorted_instance_types,
        "offerings": [],
    }

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")

    print(
        f"wrote {len(sorted_instance_types)} instance types to {args.output}",
        file=sys.stderr,
    )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
