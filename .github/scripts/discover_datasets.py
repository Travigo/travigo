#!/usr/bin/env python3
"""Discover Travigo dataset IDs from data/datasources/*.yaml."""

import glob
import json
import os
import yaml


def main():
    root = 'data/datasources'

    if not os.path.isdir(root):
        raise SystemExit('datasource directory not found: ' + root)

    paths = sorted(
        glob.glob(os.path.join(root, '**', '*.yaml'), recursive=True)
        + glob.glob(os.path.join(root, '**', '*.yml'), recursive=True)
    )

    ids = []
    for path in paths:
        with open(path, 'r', encoding='utf-8') as f:
            candidate = yaml.safe_load(f)

        if not isinstance(candidate, dict):
            continue

        source_id = candidate.get('identifier')
        if not source_id:
            continue

        for ds in candidate.get('datasets', []) or []:
            if not isinstance(ds, dict):
                continue
            if ds.get('importdestination') == 'realtime-queue':
                continue

            ds_id = ds.get('identifier')
            if not ds_id:
                continue

            ids.append(f'{source_id}-{ds_id}')

    print(json.dumps(ids))
    print('::set-output name=datasets::' + json.dumps(ids))


if __name__ == '__main__':
    main()
