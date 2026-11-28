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
    raise ValueError(name)


def start(binary, scans, parcels):
    server = subprocess.Popen([binary, '-addr', '127.0.0.1:0', '-scans', scans, '-parcels', parcels], stderr=subprocess.PIPE, text=True)
    readable, _, _ = select.select([server.stderr], [], [], 8)
    assert readable, 'station did not start'
    line = server.stderr.readline()
    endpoint = re.search(r'listening on (127\.0\.0\.1:[0-9]+)$', line)
    assert endpoint, line
    return server, 'http://' + endpoint[1]


def stop(server):
    server.terminate()
    try:
        server.wait(timeout=7)
    except subprocess.TimeoutExpired:
        server.kill()
        server.wait()


def trail(base):
    """Exercise undo/redo, both routes, and a supplementary-scan restart."""
    bench = base + '/lightbench/cinder-a'

    def call(path, expected=200, method='GET'):
        try:
            response = urlopen(Request(bench + path, method=method), timeout=8)
        except HTTPError as error:
            response = error
        with response:
            assert response.status == expected, (path, response.status, expected)
            data = response.read()
            return json.loads(data) if response.headers.get_content_type() == 'application/json' else data

    first = call('/window?start=1&end=4&profile=bench&frame=1&row=500')
    window_step = first['step']['id']
    assert first['live'] and first['step']['probe'] == {'frame': 1, 'row': 500}
    second = call('/window?start=3&end=6&profile=bench&frame=4&row=100')
    second_step = second['step']['id']
    lineage = {entry['id']: entry for entry in second['trail']['entries']}
    assert lineage[second_step]['parent'] == window_step
    assert second['step']['probe'] == {'frame': 4, 'row': 100}
    third = call('/window?start=0&end=3&profile=bench&frame=0&row=0')
    third_step = third['step']['id']
    assert {entry['id']: entry for entry in third['trail']['entries']}[third_step]['parent'] == second_step

    # The night shift withdraws two steps, landing back on the old window, then
    # tries another calibration from there. Both routes must survive the trial.
    back_one = call('/undo', method='POST')
    assert back_one['step']['id'] == second_step
    back_two = call('/undo', method='POST')
    assert back_two['step']['id'] == window_step and back_two['trail']['current'] == window_step
    # A window that does not validate is refused before touching the trail.
    assert call('/window?start=4&end=4&profile=bench', 400)
    # A failed calibration is refused the same way and nothing is recorded.
    assert call('/window?start=1&end=4&profile=absent', 422)
    after_failure = call('/applied')
    assert after_failure['step']['id'] == window_step
    # The alternative calibration is usable, so the trial line becomes a new
    # branch under the old window and the original follow-up stays redoable.
    trial = call('/window?start=1&end=4&profile=soft&frame=2&row=200')
    trial_step = trial['step']['id']
    recorded = trial['trail']
    routes = sorted(sorted(route) for route in recorded['routes'])
    assert sorted([window_step, second_step, third_step]) in routes and sorted([window_step, trial_step]) in routes, routes
    # The trial does not pan out, so the operator steps back off it onto the old
    # window and redoes the original follow-up exactly as it was.
    off_trial = call('/undo', method='POST')
    assert off_trial['step']['id'] == window_step and off_trial['trail']['current'] == window_step
    redo = call('/redo', method='POST')
    assert redo['step']['id'] == second_step, redo['step']['id']
    back = call('/redo', method='POST')
    assert back['step']['id'] == third_step

    # The abandoned trial is still listed so the lead can review it too.
    left = {entry['id']: entry for entry in back['trail']['entries']}
    assert left[trial_step]['origin']['status'] == 'reapplicable'
    assert left[trial_step]['probe'] == {'frame': 2, 'row': 200}

    # Focus moves onto the trial without deleting the original route.
    focused = call('/focus?entry=' + trial_step, method='POST')
    assert focused['step']['id'] == trial_step and focused['trail']['current'] == trial_step
    assert sorted([window_step, second_step, third_step]) in [sorted(r) for r in focused['trail']['routes']]

    # A frozen parcel pins the step's window and calibration even after another
    # window is applied.
    call('/focus?entry=' + second_step, method='POST')
    frozen = call('/freeze', 201, 'POST')
    parcel = frozen['receipt']['id']
    assert frozen['step']['parcel'] == parcel and frozen['step']['id'] == second_step
    call('/window?start=0&end=2&profile=bench')
    assert call('/history')['entries']
    again = call('/freeze', 201, 'POST')
    assert again['step']['parcel'] != parcel

    # Steps recorded against the current bytes can be re-applied; the page and
    # the probe survive a browser reload through the applied endpoint.
    call('/focus?entry=' + second_step, method='POST')
    reopened = call('/applied')
    assert reopened['step']['id'] == second_step and reopened['step']['probe'] == {'frame': 4, 'row': 100}
    assert reopened['live'] is True
    return {'steps': [entry['id'] for entry in recorded['entries']], 'routes': routes, 'parcel': parcel, 'reopened': reopened['step']['id']}


def supplement(scans, parcels, binary):
    """Register a page that was missing at apply time and review the old step."""
    server, base = start(binary, scans, parcels)
    try:
        return _supplement(scans, parcels, binary, base)
    finally:
        stop(server)


def _requestor(base, reel):
    def call(path, expected=200, method='GET'):
        try:
            response = urlopen(Request(base + '/lightbench/' + reel + path, method=method), timeout=8)
        except HTTPError as error:
            response = error
        with response:
            assert response.status == expected, (path, response.status, expected)
            data = response.read()
            return json.loads(data) if response.headers.get_content_type() == 'application/json' else data
    return call


def _supplement(scans, parcels, binary, base):
    call = _requestor(base, 'cinder-a')
    # Step 0-2 draws sound from page 2 through the lane lead, and page 2 is
    # missing, so its sound status is missing_sheet and its sheet digest is
    # empty in the footprint.
    applied = call('/window?start=0&end=2&profile=bench&frame=0&row=100')
    step_id = applied['step']['id']
    sources = {source['frame']: source['sheet_sha256'] for source in applied['step']['sources']}
    assert sources.get(2) == '', sources

    # Supply the missing page, then restart the station on the same trail. The
    # step in force is now record-only, which is exactly what a browser reopened
    # after a supplementary scan must show.
    produce_sheet(scans/'cinder-a'/'emulsion-0002.png')
    server, base = start(binary, scans, parcels)
    try:
        call = _requestor(base, 'cinder-a')
        reopened = call('/applied')
        assert reopened['live'] is False
        assert reopened['step']['id'] == step_id and reopened['step']['probe'] == {'frame': 0, 'row': 100}
        # The frozen snapshot, not the supplemented page, is what is shown.
        assert reopened['step']['sources'] == applied['step']['sources']
        assert reopened['coverage'] == applied['coverage'] and reopened['envelope'] == applied['envelope']
        # A page supplied into the sound path turns the step record-only: it
        # cannot be brought back into force onto the new bytes.
        call('/focus?entry=' + step_id, 409, 'POST')

        # A separate window that draws no sound from page 2 stays applicable,
        # and a new step is recorded on top of the fresh pages.
        safe = call('/window?start=4&end=6&profile=bench')
        safe_id = safe['step']['id']
        entries = {entry['id']: entry for entry in call('/history')['entries']}
        assert entries[step_id]['origin']['status'] == 'record_only', entries[step_id]
        assert entries[safe_id]['origin']['status'] == 'reapplicable', entries[safe_id]

        # Continuing from the applicable step records a new child; the older
        # step's window, calibration and source digests stay exactly as applied.
        grown = call('/window?start=5&end=6&profile=soft&frame=5&row=10')
        grown_id = grown['step']['id']
        lineage = {entry['id']: entry for entry in grown['trail']['entries']}
        assert lineage[grown_id]['parent'] == safe_id
        untouched = lineage[step_id]
        assert untouched['origin']['status'] == 'record_only'
        assert untouched['window'] == applied['step']['window'] and untouched['gauge'] == applied['step']['gauge']
        assert untouched['sources'] == applied['step']['sources']
        return {'record_only': step_id, 'reapplicable': safe_id, 'continued': grown_id}
    finally:
        stop(server)


def produce_sheet(path):
    """Supply a missing sample page with a byte-valid page of the same geometry.

    The sample generator deliberately omits one page per reel, so a
    supplementary scan is simulated by copying a present sheet of identical
    dimensions. Its bytes differ from anything recorded before.
    """
    donor = path.parent/'emulsion-0003.png'
    assert donor.exists(), donor
    path.write_bytes(donor.read_bytes())


def main():
    names = (
        'intake calibration register coverage trace audition compare overlay release verify '
        'workbench window-inspection strip-projection trail supplement'
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
        for name in names if args.workflow == 'all' else [args.workflow]:
            if name in ('trail', 'supplement'):
                # These record history and one supplies a page, so each needs a
                # private catalog that cannot leak into the other workflows.
                with tempfile.TemporaryDirectory(prefix='emberstripe-' + name + '-') as extra:
                    alone = Path(extra)/'scans'
                    subprocess.run(['go', 'run', './cmd/samples', '-out', alone], check=True, timeout=35)
                    if name == 'supplement':
                        observed = supplement(alone, Path(extra)/'parcels', binary)
                    else:
                        server, base = start(binary, alone, Path(extra)/'parcels')
                        try:
                            observed = trail(base)
                        finally:
                            stop(server)
                print(json.dumps({'workflow': name, 'observed': observed}, ensure_ascii=False))
                continue
            server, base = start(binary, scans, parcels)
            try:
                observed = workflow(base, name, parcels)
                print(json.dumps({'workflow': name, 'observed': observed}, ensure_ascii=False))
            finally:
                stop(server)


if __name__ == '__main__':
    main()
