"""Run the station against synthetic scanner strips using its HTTP interface."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import re
import select
import struct
import subprocess
import tempfile
from urllib.error import HTTPError
from urllib.request import Request, urlopen
import wave
import zipfile


def workflow(base, name, parcels):
    capture = '/captures/cinder-a/'

    def request(path, expected=200, method='GET'):
        try:
            response = urlopen(Request(base + path, method=method), timeout=8)
        except HTTPError as error:
            response = error
        with response:
            assert response.status == expected, (path, response.status, expected)
            data = response.read()
            return json.loads(data) if response.headers.get_content_type() == 'application/json' else data

    def get(action, expected=200):
        return request(capture + action, expected)

    def release():
        return request(capture+'release?start=1&end=4&profile=soft', 201, 'POST')

    if name == 'intake':
        assert request('/captures') == ['cinder-a', 'vireo-b']
        head = get('header')
        assert head['sample_rate'] == 48000
        assert head['available'] == [0, 1, 3, 4, 5, 6]
        assert head['depths']['4'] == 8 and head['depths']['3'] == 16
        request('/captures/nope/header', 404)
        return {'available': head['available'], 'rate': head['sample_rate']}
    if name == 'calibration':
        gauge = get('calibration?profile=bench')
        assert gauge['lanes'] == [{'dark': 6000, 'clear': 55000}, {'dark': 8200, 'clear': 50200}]
        for profile in ('flat', 'absent', 'nope'):
            get('calibration?profile='+profile, 400)
        return gauge
    if name == 'register':
        missing = get('register?frame=1&row=1770')
        assert missing['source'] == {'frame': 2, 'row': 0} and missing['status'] == 'missing_sheet'
        leading = request('/captures/vireo-b/register?frame=0&row=134')
        assert leading['status'] == 'outside_capture' and 'source' not in leading
        get('register?frame=0&row=2000', 400)
        return missing
    if name == 'coverage':
        runs = get('coverage?start=1&end=4')
        assert [(r['picture_row_start'], r['picture_row_end'], r['status']) for r in runs] == [
            (2000, 3770, 'readable'), (3770, 5770, 'missing_sheet'), (5770, 8000, 'readable')]
        get('coverage?start=4&end=4', 400)
        return runs
    if name == 'trace':
        probe = get('trace?frame=0&row=400')
        assert probe['source'] == {'frame': 0, 'row': 630} and probe['sample'][0] != probe['sample'][1]
        assert get('trace?frame=2&row=0')['sample'] == [0, 0]
        get('trace?frame=7&row=0', 400)
        return probe
    if name == 'audition':
        data = get('audition?start=1&end=4')
        with wave.open(io.BytesIO(data)) as audio:
            assert (audio.getnchannels(), audio.getsampwidth(), audio.getframerate(), audio.getnframes()) == (2, 2, 48000, 6000)
            audio.setpos(1769)
            actual = struct.unpack('<hh', audio.readframes(1))
            assert list(actual) == get('trace?frame=1&row=1769')['sample']
            assert audio.readframes(2000) == bytes(8000)
            assert audio.readframes(12) != bytes(48)
        get('audition?start=0&end=8', 400)
        return {'stereo_frames': 6000, 'missing_frames': 2000}
    if name == 'compare':
        result = get('compare?start=1&end=4&profile=bench&against=soft')
        assert result['difference']['compared_frames'] == 4000
        assert min(result['difference']['changed_samples']) > 0
        assert result['right_clipped'] != result['left_clipped']
        get('compare?start=1&end=4&against=bench', 400)
        return result['difference']
    if name == 'overlay':
        a = get('overlay?frame=2')
        b = get('overlay?frame=2&profile=soft')
        assert a[:8] == b'\x89PNG\r\n\x1a\n' and struct.unpack('>II', a[16:24]) == (160, 2000)
        assert a != b
        get('overlay?frame=-1', 400)
        return {'width': 160, 'rows': 2000, 'profile_changes_pixels': True}
    if name == 'release':
        receipt = release()
        assert release()['id'] == receipt['id']
        data = request('/parcels/'+receipt['id'])
        assert hashlib.sha256(data).hexdigest() == receipt['id']
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            assert archive.read('sound.wav') == get('audition?start=1&end=4&profile=soft')
            table = json.loads(archive.read('register.json'))
            assert table['pictures'][1] == {'frame': 2, 'sample_start': 2000}
        request(capture+'release?start=1&end=4&profile=absent', 400, 'POST')
        return {'parcel': receipt['id'], 'frame_2': table['pictures'][1]}
    if name == 'verify':
        receipt = release()
        path = '/parcels/'+receipt['id']+'/verify'
        assert request(path) == receipt
        blob = parcels / (receipt['id']+'.zip')
        corrupt = bytearray(blob.read_bytes())
        corrupt[50] ^= 1
        blob.write_bytes(corrupt)
        request(path, 409)
        request('/parcels/'+'f'*64+'/verify', 404)
        return {'verified_frames': receipt['wave']['sample_frames'], 'tamper_status': 409}
    if name == 'workbench':
        first = request('/').decode()
        assert 'data-reel="cinder-a"' in first and '画格探针' in first
        second = request('/lightbench/vireo-b').decode()
        assert 'data-reel="vireo-b"' in second and '声画偏移 -135 行' in second
        assert '不可用' in second and '/light-assets/scripts/main.js' in second
        request('/lightbench/unknown', 404)
        return {'reels': ['cinder-a', 'vireo-b'], 'unusable_profiles_visible': True}
    if name == 'window-inspection':
        path = '/lightbench/cinder-a/window?start=1&end=4&profile=bench'
        data = request(path)
        assert data['coverage'] == get('coverage?start=1&end=4')
        assert data['rate'] == 48000 and data['rows_per_frame'] == 2000
        assert [(frame['depth'], frame['readable'], frame['missing']) for frame in data['frames']] == [
            (16, 1770, 230), (0, 230, 1770), (16, 2000, 0)]
        bins = data['envelope']
        assert bins[0]['start'] == 2000 and bins[-1]['end'] == 8000
        assert sum(item['readable'] for item in bins) == 4000
        assert any(item['readable'] == 0 for item in bins)
        assert any(0 < item['readable'] < item['end'] - item['start'] for item in bins)
        assert any(item['min'] != item['max'] for item in bins if item['readable'])
        request(path.replace('profile=bench', 'profile=absent'), 422)
        request(path.replace('end=4', 'end=1'), 400)
        return {'coverage_runs': len(data['coverage']), 'readable_rows': 4000, 'envelope_bins': len(bins)}
    if name == 'strip-projection':
        prefix = '/lightbench/cinder-a/strip?frame='
        original = request(prefix+'1')
        mixed_depth = request(prefix+'4')
        assert original[:8] == b'\x89PNG\r\n\x1a\n' and struct.unpack('>II', original[16:24]) == (768, 64)
        assert original != mixed_depth
        request(prefix+'2', 404)
        request(prefix+'7', 400)
        return {'projection': '768×64', 'missing_status': 404}
    if name == 'fragment-inspection':
        prefix = '/lightbench/cinder-a/fragment?frame='
        deep = request(prefix+'1&row=400')
        assert deep['frame'] == 1 and deep['depth'] == 16
        assert (deep['row_start'], deep['row_end'], deep['selected_row']) == (368, 432, 400)
        assert len(deep['rows']) == 64 and deep['rows'][0]['row'] == 368
        for item in deep['rows']:
            assert 0 <= item['displayed_gray'] <= 255
            assert item['displayed_gray'] == item['native_gray'] // 257
            assert item['quantized_away'] == (item['native_gray'] != item['displayed_gray'] * 257)
        assert any(item['quantized_away'] for item in deep['rows'])
        edge = request(prefix+'1&row=2&span=16')
        assert (edge['row_start'], edge['row_end']) == (0, 16)
        eight = request(prefix+'4&row=100&span=32')
        assert eight['depth'] == 8 and len(eight['rows']) == 32
        for item in eight['rows']:
            assert item['native_gray'] // 257 == item['displayed_gray']
            # Averaged 8-bit pixels stay within one 8-bit step of the shade shown.
            assert abs(item['native_gray'] - item['displayed_gray'] * 257) < 257
        picture = request('/lightbench/cinder-a/fragment-image?frame=4&row=100&span=32')
        assert picture[:8] == b'\x89PNG\r\n\x1a\n'
        assert struct.unpack('>II', picture[16:24]) == (eight['image_columns'], 32)
        request(prefix+'2&row=0', 404)
        request(prefix+'7&row=0', 400)
        request(prefix+'1&row=2000', 400)
        request(prefix+'1&row=0&span=257', 400)
        request('/lightbench/cinder-a/fragment-image?frame=2&row=0', 404)
        return {'depth': 16, 'rows': len(deep['rows']), 'missing_status': 404}
    raise ValueError(name)


def main():
    names = (
        'intake calibration register coverage trace audition compare overlay release verify '
        'workbench window-inspection strip-projection fragment-inspection'
    ).split()
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('workflow', choices=['all', *names])
    args = cli.parse_args()
    with tempfile.TemporaryDirectory(prefix='emberstripe-') as tmp:
        root = Path(tmp)
        binary = root / 'emberstripe'
        scans, parcels = root/'scans', root/'parcels'
        subprocess.run(['go', 'build', '-o', binary, './cmd/emberstripe'], check=True, timeout=35)
        subprocess.run(['go', 'run', './cmd/samples', '-out', scans], check=True, timeout=35)
        with subprocess.Popen([binary, '-addr', '127.0.0.1:0', '-scans', scans, '-parcels', parcels], stderr=subprocess.PIPE, text=True) as server:
            try:
                readable, _, _ = select.select([server.stderr], [], [], 8)
                assert readable, 'station did not start'
                line = server.stderr.readline()
                endpoint = re.search(r'listening on (127\.0\.0\.1:[0-9]+)$', line)
                assert endpoint, line
                for name in names if args.workflow == 'all' else [args.workflow]:
                    print(json.dumps({'workflow': name, 'observed': workflow('http://'+endpoint[1], name, parcels)}, ensure_ascii=False))
            finally:
                server.terminate()
                try:
                    server.wait(timeout=7)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()


if __name__ == '__main__':
    main()
