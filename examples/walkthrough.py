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
        assert request('/captures') == ['cinder-a', 'cinder-a-rescan', 'vireo-b']
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
    if name == 'relight':
        listing = request('/relight/records')
        assert [item['id'] for item in listing] == [
            'relight-aligned', 'relight-corner', 'relight-diverged', 'relight-gap']
        for item in listing:
            assert item['geometry_match'] is True
            assert item['whole_reel_release_allowed'] == (item['verdict'] == 'aligned')
            assert [zone['zone'] for zone in item['zone_verdicts']] == [
                'picture', 'left-lane', 'right-lane', 'film-edge']
            assert [mat['material'] for mat in item['materials']] == [
                'clear-base', 'middle-strip', 'reference']
            for mat in item['materials']:
                assert len(mat['cells']) == 4
                for cell in mat['cells']:
                    for scan in ('original', 'rescan'):
                        reading = cell[scan]
                        if reading['available']:
                            assert reading['samples'] > 0 and reading['depth'] in (8, 16)
                            assert reading['scale16_min'] <= reading['mean_gray16'] <= reading['scale16_max']
                            if reading['depth'] == 16:
                                assert reading['native_min'] == reading['scale16_min']
                            else:
                                assert reading['native_max'] <= 255
        aligned = request('/relight/records/relight-aligned')
        assert aligned['verdict'] == 'aligned' and aligned['whole_reel_release_allowed'] is True
        assert all(zone['verdict'] == 'aligned' for zone in aligned['zone_verdicts'])
        assert all(cell['verdict'] == 'aligned'
                   for mat in aligned['materials'] for cell in mat['cells'])

        diverged = request('/relight/records/relight-diverged')
        assert diverged['verdict'] == 'diverged' and diverged['whole_reel_release_allowed'] is False
        strip = next(mat for mat in diverged['materials'] if mat['material'] == 'middle-strip')
        right = next(cell for cell in strip['cells'] if cell['zone'] == 'right-lane')
        assert right['verdict'] == 'diverged' and abs(right['mean_delta_gray16']) > 256
        edge = next(cell for cell in strip['cells'] if cell['zone'] == 'film-edge')
        assert edge['verdict'] == 'aligned'
        zone_map = {zone['zone']: zone['verdict'] for zone in diverged['zone_verdicts']}
        assert zone_map['right-lane'] == 'diverged' and zone_map['film-edge'] == 'aligned'

        gap = request('/relight/records/relight-gap')
        assert gap['verdict'] == 'insufficient' and gap['whole_reel_release_allowed'] is False
        gap_strip = next(mat for mat in gap['materials'] if mat['material'] == 'middle-strip')
        for cell in gap_strip['cells']:
            assert cell['verdict'] == 'insufficient'
            assert cell['original']['available'] is False
            assert cell['original']['reason'] == 'missing_sheet'

        corner = request('/relight/records/relight-corner')
        assert corner['verdict'] == 'insufficient' and corner['whole_reel_release_allowed'] is False
        corner_ref = next(mat for mat in corner['materials'] if mat['material'] == 'reference')
        cells = {cell['zone']: cell for cell in corner_ref['cells']}
        assert cells['left-lane']['verdict'] == 'insufficient'
        assert cells['left-lane']['original']['reason'] == 'damaged_corner'
        assert cells['right-lane']['verdict'] == 'aligned'
        for zone in ('picture', 'right-lane', 'film-edge'):
            assert cells[zone]['original']['available'] is True
        corner_map = {zone['zone']: zone['verdict'] for zone in corner['zone_verdicts']}
        assert corner_map['left-lane'] == 'insufficient' and corner_map['right-lane'] == 'aligned'

        request('/relight/records/nope', 404)

        page = request('/lightbench/relight').decode()
        assert '跨扫描读光记录' in page
        assert '整卷不得沿用同一验收线' in page
        assert '可复算的原始读数' in page
        return {'records': len(listing), 'release_allowed': aligned['whole_reel_release_allowed']}
    raise ValueError(name)


def main():
    names = (
        'intake calibration register coverage trace audition compare overlay release verify '
        'workbench window-inspection strip-projection relight'
    ).split()
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('workflow', choices=['all', *names])
    args = cli.parse_args()
    with tempfile.TemporaryDirectory(prefix='emberstripe-') as tmp:
        root = Path(tmp)
        binary = root / 'emberstripe'
        scans, parcels, records = root/'scans', root/'parcels', root/'relight'
        subprocess.run(['go', 'build', '-o', binary, './cmd/emberstripe'], check=True, timeout=35)
        subprocess.run(['go', 'run', './cmd/samples', '-out', scans, '-records', records], check=True, timeout=35)
        with subprocess.Popen([binary, '-addr', '127.0.0.1:0', '-scans', scans,
                               '-records', records, '-parcels', parcels],
                              stderr=subprocess.PIPE, text=True) as server:
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
