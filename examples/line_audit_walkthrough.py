"""Reproduce the two-trial line-audit workflow through the running station."""

import io
import json
import os
from pathlib import Path
import re
import select
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
import wave


def main():
    with tempfile.TemporaryDirectory(prefix='emberstripe-pair-') as tmp_name:
        tmp = Path(tmp_name)
        binary = tmp / 'emberstripe'
        scans, parcels = tmp / 'scans', tmp / 'parcels'
        env = os.environ.copy()
        root = Path(__file__).resolve().parents[1]
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/emberstripe'], cwd=root, check=True, timeout=60, env=env)
        subprocess.run(['go', 'run', './cmd/pairsamples', '-out', str(scans)], cwd=root, check=True, timeout=60, env=env)
        server = subprocess.Popen(
            [binary, '-addr', '127.0.0.1:0', '-scans', str(scans), '-parcels', str(parcels)],
            stderr=subprocess.PIPE, text=True, env=env,
        )
        try:
            readable, _, _ = select.select([server.stderr], [], [], 8)
            assert readable, 'station did not start'
            line = server.stderr.readline()
            match = re.search(r'listening on (127\.0\.0\.1:\d+)$', line)
            assert match, line
            base = 'http://' + match.group(1)

            def get(path, expected=200):
                try:
                    response = urllib.request.urlopen(base + path, timeout=15)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    body = response.read()
                    assert response.status == expected, (path, response.status, expected, body[:200])
                    return json.loads(body) if 'application/json' in response.headers.get_content_type() else body

            report = get('/captures/line-audit?a=trial-a&b=trial-b&profile=bench')
            assert abs(report['global_shift_rows'] - 36) <= 8
            deltas = {f['delta_rows']: f for f in report['suspects']}
            assert -28 in deltas and 36 in deltas, deltas.keys()
            assert report['missing_pages'], 'physical missing page was not reported'
            deletion = deltas[-28]
            assert deletion['address_a']['frame'] == 1
            assert deletion['links_a']['audition_url'].startswith('/captures/trial-a/row-audition')
            wav = get(deletion['links_a']['audition_url'])
            with wave.open(io.BytesIO(wav)) as audio:
                assert (audio.getnchannels(), audio.getframerate()) == (2, 48000)
            page = get('/lightbench/pair').decode()
            assert '试扫行距对拍' in page and 'pair.js' in page
            print(json.dumps({
                'global_shift_rows': report['global_shift_rows'],
                'suspects': [(f['kind'], f['delta_rows'], f['a_interval']) for f in report['suspects']],
                'missing_pages': len(report['missing_pages']),
                'audition_frames': audio.getnframes(),
            }, ensure_ascii=False))
        finally:
            server.terminate()
            try:
                server.wait(timeout=5)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait()


if __name__ == '__main__':
    try:
        main()
    except AssertionError as error:
        print('line-audit walkthrough failed:', error, file=sys.stderr)
        raise
