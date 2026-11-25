"""Exercise audit-card creation and the three provenance states on reopen.

The script never needs Pillow: a "rescanned" sheet is produced by recompressing
an existing PNG's IDAT stream, which changes the file digest without changing
pixels, geometry or bit depth, exactly like a same-name reel re-scanned into
different bytes.
"""
import binascii
import hashlib
import json
import re
import select
import shutil
import struct
import subprocess
import tempfile
import zlib
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

REEL = 'cinder-a'
DIGEST_RE = re.compile(r'^[a-f0-9]{64}$')


def http(base, path, payload=None, method='GET', expected=200):
    headers = {}
    if payload is not None:
        body = payload if isinstance(payload, (bytes, bytearray)) else json.dumps(payload).encode()
        headers['Content-Type'] = 'application/json'
    elif method == 'POST':
        body = b''
    else:
        body = None
    try:
        response = urlopen(Request(base + path, data=body, headers=headers, method=method), timeout=10)
    except HTTPError as error:
        response = error
    with response:
        assert response.status == expected, (path, response.status, expected, response.read()[:200])
        data = response.read()
        return json.loads(data) if response.headers.get_content_type() == 'application/json' else data


def recompress_png(path):
    """Return PNG bytes with a differently compressed IDAT but identical image."""
    raw = Path(path).read_bytes()
    assert raw[:8] == b'\x89PNG\r\n\x1a\n'
    pos, pieces, idat = 8, [], []
    while pos < len(raw):
        size = struct.unpack('>I', raw[pos:pos + 4])[0]
        kind = raw[pos + 4:pos + 8]
        chunk = raw[pos + 8:pos + 8 + size]
        pos += 12 + size
        if kind == b'IDAT':
            idat.append(chunk)
            continue
        pieces.append((kind, chunk))
    image = zlib.decompress(b''.join(idat))
    rebuilt_levels = [level for level in range(10) if level != 6]
    output = raw[:8]
    for kind, chunk in pieces:
        if kind == b'IDAT':
            continue
        if kind == b'IEND':
            new_idat = zlib.compress(image, rebuilt_levels[0])
            output += struct.pack('>I', len(new_idat)) + b'IDAT' + new_idat + struct.pack('>I', binascii.crc32(b'IDAT' + new_idat))
        output += struct.pack('>I', len(chunk)) + kind + chunk + struct.pack('>I', binascii.crc32(kind + chunk))
    assert output != raw
    return output


def start_server(binary, scans, parcels):
    server = subprocess.Popen([binary, '-addr', '127.0.0.1:0', '-scans', scans, '-parcels', parcels],
                              stderr=subprocess.PIPE, stdout=subprocess.DEVNULL, text=True)
    readable, _, _ = select.select([server.stderr], [], [], 8)
    assert readable, 'station did not start'
    match = re.search(r'listening on (127\.0\.0\.1:[0-9]+)$', server.stderr.readline())
    assert match, 'no listening endpoint'
    return server, 'http://' + match[1]


def main():
    with tempfile.TemporaryDirectory(prefix='emberstripe-card-') as tmp:
        root = Path(tmp)
        binary = root / 'emberstripe'
        scans_a, scans_b, scans_c = root / 'scans-a', root / 'scans-b', root / 'scans-c'
        parcels, parcels_empty, parcels_bad = root / 'parcels', root / 'parcels-empty', root / 'parcels-bad'
        subprocess.run(['go', 'build', '-o', binary, './cmd/emberstripe'], check=True, timeout=40)
        subprocess.run(['go', 'run', './cmd/samples', '-out', scans_a], check=True, timeout=40)

        server, base = start_server(binary, scans_a, parcels)
        try:
            header = http(base, f'/captures/{REEL}/header')
            window = http(base, f'/lightbench/{REEL}/window?start=1&end=4&profile=bench')
            assert DIGEST_RE.match(window['source_sha256']) and window['source_sha256'] == header['sha256']
            page = http(base, '/lightbench/audit-card')
            assert 'card-open-form'.encode() in page and b'audit-open.js' in page

            note = '第 2 格入口疑似爆点，下一班复听'
            created = http(base, f'/lightbench/{REEL}/audit-card', {
                'start': 1, 'end': 4, 'profile': 'bench', 'frame': 2, 'row': 100, 'note': note,
            }, 'POST', 201)
            card = created['card']
            assert card['version'] == 1 and card['reel'] == REEL
            assert card['source_sha256'] == header['sha256']
            assert card['profile'] == 'bench' and card['window'] == {'start': 1, 'end': 4}
            assert card['cursor'] == {'frame': 2, 'row': 100}
            assert card['note'] == note and DIGEST_RE.match(card['parcel_id']) and DIGEST_RE.match(card['audio_sha256'])
            json.loads(json.dumps(card))  # card is a portable JSON document
            assert created['parcel_id'] == card['parcel_id']

            receipt = http(base, '/parcels/' + card['parcel_id'] + '/verify')
            assert receipt['index']['source_sha256'] == card['source_sha256']
            wav = http(base, '/parcels/' + card['parcel_id'] + '/sound')
            assert hashlib.sha256(wav).hexdigest() == card['audio_sha256']
            assert wav[:4] == b'RIFF' and len(wav) == 44 + 6000 * 4

            # Rejected card requests never publish anything.
            http(base, f'/lightbench/{REEL}/audit-card', {'start': 0, 'end': 99, 'profile': 'bench'}, 'POST', 400)
            http(base, f'/lightbench/{REEL}/audit-card', {'start': 1, 'end': 4, 'profile': 'bench', 'frame': 7, 'row': 0}, 'POST', 400)
            http(base, f'/lightbench/{REEL}/audit-card', {'start': 1, 'end': 4, 'profile': 'absent'}, 'POST', 422)
            http(base, f'/lightbench/{REEL}/audit-card', {'start': 1, 'end': 4, 'profile': 'bench', 'stray': 1}, 'POST', 400)
            http(base, '/lightbench/nope/audit-card', {'start': 1, 'end': 4, 'profile': 'bench'}, 'POST', 404)
            http(base, '/parcels/' + 'f' * 64 + '/sound', expected=404)

            # Same registered material: original pixels remain inspectable.
            ready = http(base, '/lightbench/audit-card/open', card, 'POST')
            assert ready['status'] == 'pixel_ready'
            assert ready['reel_present'] and ready['reel_digest_match']
            assert ready['parcel_verified'] and ready['parcel_match']
            assert ready['sound_url'] == '/parcels/' + card['parcel_id'] + '/sound'
            assert f'/lightbench/{REEL}?' in ready['continue_url']
            assert 'frame=2' in ready['continue_url'] and 'row=100' in ready['continue_url']
            resumed = http(base, ready['continue_url'])
            assert f'data-reel="{REEL}"'.encode() in resumed

            # A card whose digest was altered cannot claim the live reel or parcel.
            tampered = dict(card, source_sha256='0' * 64)
            rejected = http(base, '/lightbench/audit-card/open', tampered, 'POST')
            assert rejected['status'] == 'unverified'
            assert not rejected['reel_digest_match'] and not rejected['parcel_match']
            assert 'sound_url' not in rejected and 'continue_url' not in rejected
            http(base, '/lightbench/audit-card/open', b'{"version": 2}', 'POST', 400)
            http(base, '/lightbench/audit-card/open', dict(card, cursor={'frame': 9, 'row': 0}), 'POST', 400)
            http(base, '/lightbench/audit-card/open', dict(card, extra=1), 'POST', 400)
        finally:
            server.terminate()
            server.wait(timeout=7)

        # Same reel name, same geometry, but rescanned into different bytes.
        shutil.copytree(scans_a, scans_b)
        target = scans_b / REEL / 'emulsion-0004.png'
        original_bytes = target.read_bytes()
        target.write_bytes(recompress_png(target))
        assert hashlib.sha256(target.read_bytes()).hexdigest() != hashlib.sha256(original_bytes).hexdigest()
        shutil.copytree(scans_a / 'vireo-b', scans_c / 'vireo-b', dirs_exist_ok=True)
        shutil.copytree(parcels, parcels_bad)
        bad_blob = parcels_bad / (card['parcel_id'] + '.zip')
        corrupted = bytearray(bad_blob.read_bytes())
        corrupted[60] ^= 1
        bad_blob.write_bytes(corrupted)
        parcels_empty.mkdir()

        server, base = start_server(binary, scans_b, parcels)
        try:
            rescanned = http(base, f'/captures/{REEL}/header')
            assert rescanned['name'] == REEL and rescanned['sha256'] != card['source_sha256']
            assessment = http(base, '/lightbench/audit-card/open', card, 'POST')
            # Identical name and window length must not resurrect old evidence.
            assert assessment['status'] == 'frozen_audio'
            assert assessment['reel_present'] and not assessment['reel_digest_match']
            assert assessment['parcel_verified'] and assessment['parcel_match']
            assert 'continue_url' not in assessment and assessment['sound_url']
            sound = http(base, assessment['sound_url'])
            assert hashlib.sha256(sound).hexdigest() == card['audio_sha256']
        finally:
            server.terminate()
            server.wait(timeout=7)

        # Only the frozen package remains: the reel is no longer registered.
        server, base = start_server(binary, scans_c, parcels)
        try:
            frozen = http(base, '/lightbench/audit-card/open', card, 'POST')
            assert frozen['status'] == 'frozen_audio'
            assert not frozen['reel_present'] and frozen['parcel_match']
            assert http(base, frozen['sound_url'])[:4] == b'RIFF'
        finally:
            server.terminate()
            server.wait(timeout=7)

        # Rescanned reel plus a missing or corrupted package: nothing proves origin.
        server, base = start_server(binary, scans_b, parcels_empty)
        try:
            no_proof = http(base, '/lightbench/audit-card/open', card, 'POST')
            assert no_proof['status'] == 'unverified'
            assert no_proof['reel_present'] and not no_proof['parcel_verified']
            assert 'sound_url' not in no_proof and 'continue_url' not in no_proof
        finally:
            server.terminate()
            server.wait(timeout=7)

        server, base = start_server(binary, scans_b, parcels_bad)
        try:
            corrupt_proof = http(base, '/lightbench/audit-card/open', card, 'POST')
            assert corrupt_proof['status'] == 'unverified' and not corrupt_proof['parcel_verified']
            http(base, '/parcels/' + card['parcel_id'] + '/sound', expected=409)
        finally:
            server.terminate()
            server.wait(timeout=7)

        print(json.dumps({
            'states_observed': ['pixel_ready', 'frozen_audio', 'unverified'],
            'rescanned_reel_kept_name': True,
            'digest_guarded_evidence': True,
        }, ensure_ascii=False))


if __name__ == '__main__':
    main()
