"""听审会复查清单：随实际听到的内容更新的对照证据记录。

会前把需要两段原声相互印证的位置登记为候选对照，每对的两侧各自带不可少的
前后文行；会中逐条记录实际播放（提前结束、跳过、跳到末尾、换标定重听、缺页
补零都如实登记），只有真正播到的可读行才算证据；随时可问"下一段该听什么"，
会后报告哪些判断仍只听到一边。候选清单只是计划，结论只来自实际播放事件。

用法：
    python3 examples/listening_review.py demo
    python3 examples/listening_review.py init --dir var/review
    python3 examples/listening_review.py plan   --materials M.json --plan P.json
    python3 examples/listening_review.py next   --materials M.json --plan P.json --session S.json
    python3 examples/listening_review.py play   --materials M.json --plan P.json --session S.json \
        --reel cinder-a --profile bench --start 6140 --end 6460 \
        [--stop-at 6250] [--ended early|jumped|completed] [--skip 6200:6250] [--note 备注]
    python3 examples/listening_review.py report --materials M.json --plan P.json --session S.json
"""
import argparse
import json
from pathlib import Path

STATUS_CN = {'readable': '可读', 'missing_sheet': '缺页', 'outside_capture': '卷外'}
ENDED_CN = {'completed': '播完', 'early': '提前结束', 'jumped': '跳到末尾'}


# ---------------------------------------------------------------- 区间工具

def merge(spans):
    """合并半开区间 [(s, e), ...]，返回排序后不重叠的列表。"""
    out = []
    for s, e in sorted(spans):
        if e <= s:
            continue
        if out and s <= out[-1][1]:
            out[-1] = (out[-1][0], max(out[-1][1], e))
        else:
            out.append((s, e))
    return out


def subtract(spans, cuts):
    """从 spans 中扣除 cuts，返回剩余的半开区间。"""
    cuts = merge(cuts)
    rest = []
    for s, e in merge(spans):
        parts = [(s, e)]
        for cs, ce in cuts:
            nxt = []
            for ps, pe in parts:
                if ce <= ps or cs >= pe:
                    nxt.append((ps, pe))
                    continue
                if ps < cs:
                    nxt.append((ps, cs))
                if ce < pe:
                    nxt.append((ce, pe))
            parts = nxt
        rest.extend(parts)
    return rest


def rows_of(spans):
    return sum(e - s for s, e in spans)


def fmt_spans(spans):
    return '、'.join(f'[{s},{e})' for s, e in spans)


# ---------------------------------------------------------------- 材料（卷）

class Reel:
    """一卷扫描材料：画格行时钟、声槽偏移、缺页与可用标定。

    与工位 register 的约定一致：画格行 p 的源声槽行为 p + lead_rows；源行落在
    缺页上为 missing_sheet，落在卷外为 outside_capture。只有可读行才能当证据。
    """

    def __init__(self, name, rows_per_frame, fps, frames, lead_rows, missing_frames, profiles):
        self.name = name
        self.rows_per_frame = rows_per_frame
        self.fps = fps
        self.frames = frames
        self.lead_rows = lead_rows
        self.missing_frames = set(missing_frames)
        self.profiles = tuple(profiles)

    @property
    def total_rows(self):
        return self.rows_per_frame * self.frames

    def status_at(self, picture_row):
        if picture_row < 0 or picture_row >= self.total_rows:
            return 'outside_capture'
        source = picture_row + self.lead_rows
        if source < 0 or source >= self.total_rows:
            return 'outside_capture'
        if (source // self.rows_per_frame) in self.missing_frames:
            return 'missing_sheet'
        return 'readable'

    def status_runs(self, start, end):
        """把画格行区间 [start, end) 切成连续同状态小段。"""
        runs = []
        row = start
        while row < end:
            status = self.status_at(row)
            source = row + self.lead_rows
            if status == 'outside_capture' and 0 <= source < self.total_rows:
                nxt = min(end, self.total_rows)
            elif status == 'outside_capture' and source < 0:
                nxt = min(end, -self.lead_rows)
            elif status == 'outside_capture':
                nxt = end
            else:
                nxt = min(end, row + self.rows_per_frame - source % self.rows_per_frame)
            runs.append((row, nxt, status))
            row = nxt
        return runs

    def frame_row(self, picture_row):
        return divmod(picture_row, self.rows_per_frame)


class Materials:
    def __init__(self, reels):
        self.reels = {reel.name: reel for reel in reels}

    @classmethod
    def from_dict(cls, data):
        return cls(Reel(**item) for item in data['reels'])

    def reel(self, name):
        try:
            return self.reels[name]
        except KeyError:
            raise ValueError(f'未知卷 {name!r}；已登记：{"、".join(sorted(self.reels))}')


# 与 cmd/samples 生成的两卷一致：cinder-a 缺源页 2、vireo-b 缺源页 3。
# flat/absent 在工位上不可用，这里只列可用标定 bench/soft。
DEMO_MATERIALS = {
    'reels': [
        {'name': 'cinder-a', 'rows_per_frame': 2000, 'fps': 24, 'frames': 7,
         'lead_rows': 230, 'missing_frames': [2], 'profiles': ['bench', 'soft']},
        {'name': 'vireo-b', 'rows_per_frame': 1920, 'fps': 25, 'frames': 7,
         'lead_rows': -135, 'missing_frames': [3], 'profiles': ['bench', 'soft']},
    ],
}


# ---------------------------------------------------------------- 会前候选清单

class Side:
    """对照的一侧：某卷某标定下的一段目标行，加不可少的前后文行。"""

    def __init__(self, reel, profile, frame, row, span, before, after):
        self.reel = reel
        self.profile = profile
        self.frame = frame
        self.row = row
        self.span = span
        self.before = before
        self.after = after

    @classmethod
    def from_dict(cls, data):
        return cls(data['reel'], data['profile'], data['frame'], data['row'],
                   data['span'], data.get('before', 0), data.get('after', 0))

    def validate(self, materials):
        reel = materials.reel(self.reel)
        if self.profile not in reel.profiles:
            raise ValueError(f'标定 {self.profile!r} 在卷 {self.reel} 上不可用')
        if not 0 <= self.frame < reel.frames:
            raise ValueError(f'画格 {self.frame} 超出卷 {self.reel} 的 0..{reel.frames - 1}')
        if not 0 <= self.row < reel.rows_per_frame:
            raise ValueError(f'行 {self.row} 超出每格行数 {reel.rows_per_frame}')
        if self.span <= 0 or self.before < 0 or self.after < 0:
            raise ValueError('span 须为正，前后文行数不得为负')

    def required(self, materials):
        """必须真正听到的画格行区间（目标行加前后文），半开。"""
        reel = materials.reel(self.reel)
        base = self.frame * reel.rows_per_frame + self.row
        return (base - self.before, base + self.span + self.after)


class Pair:
    def __init__(self, pid, note, a, b):
        self.pid = pid
        self.note = note
        self.a = a
        self.b = b


class Plan:
    """会前候选对照清单。它只是计划，不等于已听记录。"""

    def __init__(self, title, pairs):
        self.title = title
        self.pairs = pairs

    @classmethod
    def from_dict(cls, data, materials):
        pairs = []
        seen = set()
        for item in data['pairs']:
            if item['id'] in seen:
                raise ValueError(f'对照编号重复：{item["id"]}')
            seen.add(item['id'])
            a, b = Side.from_dict(item['a']), Side.from_dict(item['b'])
            a.validate(materials)
            b.validate(materials)
            pairs.append(Pair(item['id'], item.get('note', ''), a, b))
        return cls(data.get('title', '未命名听审会'), pairs)

    def candidates_text(self, materials):
        lines = [f'会前候选对照 {len(self.pairs)} 对（仅为计划，不代表已听）：']
        for pair in self.pairs:
            lines.append(f'{pair.pid}「{pair.note}」')
            for tag, side in (('A', pair.a), ('B', pair.b)):
                need = side.required(materials)
                if side.before == side.after:
                    ctx = f'前后文各 {side.before} 行'
                else:
                    ctx = f'前文 {side.before} 行、后文 {side.after} 行'
                lines.append(f'  {tag} 侧 {side.reel}/{side.profile}：'
                             f'目标 画格{side.frame}·行{side.row} 起 {side.span} 行，{ctx}'
                             f' → 需听画格行 [{need[0]},{need[1]})')
        return '\n'.join(lines)


DEMO_PLAN = {
    'title': '修复听审会 2026-11-27',
    'pairs': [
        {'id': 'P1', 'note': '片头提示音与场记板，两卷互证',
         'a': {'reel': 'cinder-a', 'profile': 'bench', 'frame': 0, 'row': 1700,
               'span': 200, 'before': 60, 'after': 60},
         'b': {'reel': 'vireo-b', 'profile': 'soft', 'frame': 1, 'row': 580,
               'span': 200, 'before': 80, 'after': 40}},
        {'id': 'P2', 'note': '同段落另一版，A 侧落在 P1.B 的区段里',
         'a': {'reel': 'vireo-b', 'profile': 'soft', 'frame': 1, 'row': 580,
               'span': 120, 'before': 40, 'after': 40},
         'b': {'reel': 'cinder-a', 'profile': 'bench', 'frame': 3, 'row': 200,
               'span': 200, 'before': 60, 'after': 60}},
        {'id': 'P3', 'note': '穿过缺页的对白',
         'a': {'reel': 'cinder-a', 'profile': 'bench', 'frame': 2, 'row': 1600,
               'span': 300, 'before': 50, 'after': 50},
         'b': {'reel': 'vireo-b', 'profile': 'bench', 'frame': 0, 'row': 900,
               'span': 200, 'before': 50, 'after': 50}},
    ],
}


# ---------------------------------------------------------------- 会中记录

class Session:
    """一场听审会的实际播放记录。证据只能从 play 事件累积，不能从清单誊写。"""

    def __init__(self, materials, plan):
        self.materials = materials
        self.plan = plan
        self.events = []
        self.evidence = {}  # (reel, profile) -> 真正播到的可读画格行区间

    def play(self, reel_name, profile, start, end, stop_at=None, skip=(), ended=None, note=''):
        """登记一次播放。返回如实分解后的事件：有效、补零、跳过、未播。"""
        reel = self.materials.reel(reel_name)
        if profile not in reel.profiles:
            raise ValueError(f'标定 {profile!r} 在卷 {reel_name} 上不可用')
        if not 0 <= start < end:
            raise ValueError(f'窗口非法：[{start}, {end})')
        stop = end if stop_at is None else stop_at
        if not start <= stop <= end:
            raise ValueError(f'停止位置 {stop} 不在窗口 [{start}, {end}) 内')
        if ended is None:
            ended = 'completed' if stop == end else 'early'
        if ended not in ENDED_CN:
            raise ValueError(f'未知结束方式 {ended!r}')
        if (stop == end) != (ended == 'completed'):
            raise ValueError('播到底才是 completed；提前结束或跳到末尾必须给出 stop_at')
        cuts = merge((max(s, start), min(e, stop)) for s, e in skip)
        heard, zeroed = [], []
        for s, e in subtract([(start, stop)], cuts):
            for rs, re_, status in reel.status_runs(s, e):
                if status == 'readable':
                    heard.append((rs, re_))
                else:
                    zeroed.append((rs, re_, status))
        zeroed_detail = {}
        for rs, re_, status in zeroed:
            zeroed_detail[status] = merge(zeroed_detail.get(status, []) + [(rs, re_)])
        event = {
            'seq': len(self.events) + 1,
            'reel': reel_name, 'profile': profile,
            'window': (start, end), 'stop_at': stop, 'ended': ended,
            'skip': cuts,
            'heard': merge(heard),
            'zeroed': merge((s, e) for s, e, _ in zeroed),
            'zeroed_detail': zeroed_detail,
            'unplayed': merge([(stop, end)]),
            'note': note,
        }
        self.events.append(event)
        key = (reel_name, profile)
        self.evidence[key] = merge(self.evidence.get(key, []) + event['heard'])
        return event

    # ---- 证据查询 ----

    def side_missing(self, side):
        """一侧还缺哪些画格行：需求减去同卷同标定下真正播到的可读行。"""
        need = side.required(self.materials)
        got = self.evidence.get((side.reel, side.profile), [])
        return subtract([need], got)

    def split_fillable(self, side, spans):
        """把缺口分成可补（本卷可读）与不可补（缺页/卷外，补零不算证据）。"""
        reel = self.materials.reel(side.reel)
        fillable, stuck = [], []
        for s, e in spans:
            for rs, re_, status in reel.status_runs(s, e):
                if status == 'readable':
                    fillable.append((rs, re_))
                else:
                    stuck.append((rs, re_, status))
        return merge(fillable), stuck

    def pair_state(self, pair):
        miss_a = self.side_missing(pair.a)
        miss_b = self.side_missing(pair.b)
        if not miss_a and not miss_b:
            return 'complete'
        if not miss_a or not miss_b:
            return 'half'
        return 'open'

    def next_segment(self):
        """下一段该听的内容：按计划顺序找第一对未齐的，先补 A 侧再补 B 侧。

        证据按（卷, 标定）全局共享：之前播放满足过别的对的一侧，这里直接跳过。
        """
        blocked = []
        for pair in self.plan.pairs:
            for tag, side in (('a', pair.a), ('b', pair.b)):
                missing = self.side_missing(side)
                if not missing:
                    continue
                fillable, stuck = self.split_fillable(side, missing)
                blocked.extend((pair.pid, tag, span) for span in stuck)
                if fillable:
                    return {'pair': pair.pid, 'side': tag,
                            'reel': side.reel, 'profile': side.profile,
                            'window': fillable[0], 'pieces': fillable,
                            'blocked': blocked}
        return {'pair': None, 'blocked': blocked}

    # ---- 会后报告 ----

    def report(self):
        pairs = self.plan.pairs
        lines = [f'听审复查报告 —— {self.plan.title}',
                 f'依据：{len(self.events)} 条实际播放事件。会前候选清单共 {len(pairs)} 对；'
                 '候选清单本身不代表已听，以下结论只来自播放事件。']
        half, open_ids, complete = [], [], 0
        for pair in pairs:
            state = self.pair_state(pair)
            if state == 'complete':
                complete += 1
                lines.append(f'  {pair.pid}「{pair.note}」：双耳齐全')
                continue
            miss = {'a': self.side_missing(pair.a), 'b': self.side_missing(pair.b)}
            if state == 'half':
                half.append(pair.pid)
                which = 'A' if not miss['a'] else 'B'
                lines.append(f'  {pair.pid}「{pair.note}」：仅 {which} 侧齐全')
            else:
                open_ids.append(pair.pid)
                lines.append(f'  {pair.pid}「{pair.note}」：两侧都未听齐')
            for tag, side in (('a', pair.a), ('b', pair.b)):
                need = side.required(self.materials)
                if not miss[tag]:
                    lines.append(f'    {tag.upper()} 侧 {side.reel}/{side.profile}：'
                                 f'已听齐 [{need[0]},{need[1]})')
                    continue
                covered = subtract([need], miss[tag])
                fillable, stuck = self.split_fillable(side, miss[tag])
                detail = f'    {tag.upper()} 侧 {side.reel}/{side.profile}：需 [{need[0]},{need[1]})'
                detail += f'，已听 {rows_of(covered)} 行' if covered else '，未播'
                if fillable:
                    detail += f'，仍缺 {fmt_spans(fillable)}'
                for s, e, status in stuck:
                    detail += f'，[{s},{e}) 落在{STATUS_CN[status]}上（补零不算证据）'
                lines.append(detail)
        lines.append(f'小结：双耳齐全 {complete} 对；仍只听到一边 {len(half)} 对'
                     f'（{"、".join(half) if half else "无"}）；未开始 {len(open_ids)} 对。')
        return '\n'.join(lines)


# ---------------------------------------------------------------- 展示

def show_event(materials, event):
    bits = [f"#{event['seq']:02d} {event['reel']}/{event['profile']} "
            f"窗口 [{event['window'][0]},{event['window'][1]})"]
    if event['ended'] != 'completed':
        bits.append(f"{ENDED_CN[event['ended']]}（止于 {event['stop_at']}）")
    if event['skip']:
        bits.append(f"跳过 {fmt_spans(event['skip'])}")
    bits.append(f"有效 {rows_of(event['heard'])} 行")
    if event['zeroed']:
        detail = '、'.join(f'{STATUS_CN[status]}补零 {rows_of(spans)} 行'
                           for status, spans in sorted(event['zeroed_detail'].items()))
        bits.append(f'{detail}（不算证据）')
    if event['note']:
        bits.append(event['note'])
    print('  ' + '；'.join(bits))


def next_lines(materials, nxt):
    """把 next_segment 的结果转成（提示行列表, 主推荐行）。"""
    notes = []
    for pid, tag, (s, e, status) in nxt['blocked']:
        notes.append(f'注意：{pid} {tag.upper()} 侧缺 [{s},{e})，落在{STATUS_CN[status]}上，'
                     '本卷补零不算证据，需补扫重新登记后再议')
    if nxt['pair'] is None:
        return notes, '没有可补听的段落'
    reel = materials.reel(nxt['reel'])
    s, e = nxt['window']
    f0, r0 = reel.frame_row(s)
    f1, r1 = reel.frame_row(e - 1)
    main = (f"{nxt['pair']} {nxt['side'].upper()} 侧 → {nxt['reel']}/{nxt['profile']} "
            f"画格行 [{s},{e})（画格{f0}·行{r0} 至 画格{f1}·行{r1}，共 {e - s} 行）")
    if len(nxt['pieces']) > 1:
        main += f'；之后还有 {len(nxt["pieces"]) - 1} 段缺口'
    return notes, main


def print_next(session, prefix='    下一段：'):
    notes, main = next_lines(session.materials, session.next_segment())
    for line in notes:
        print('    ' + line)
    print(prefix + main)


# ---------------------------------------------------------------- 演示

def demo():
    """同一组材料、同一份候选清单：两个叫停位置、各类无效证据、会后报告。"""
    materials = Materials.from_dict(DEMO_MATERIALS)
    plan = Plan.from_dict(DEMO_PLAN, materials)

    print('== 会前：同一组材料，同一份候选对照清单 ==')
    print(plan.candidates_text(materials))

    print('\n== 分支演示：P1.A 这段被叫停，两个不同位置 ==')
    need_a = plan.pairs[0].a.required(materials)  # (1640, 1960)

    sess_x = Session(materials, plan)
    show_event(materials, sess_x.play('cinder-a', 'bench', need_a[0], need_a[1],
                                      stop_at=1800, ended='early', note='分支甲：1800 处叫停'))
    nxt_x = sess_x.next_segment()
    print_next(sess_x, prefix='    分支甲下一段：')

    sess_y = Session(materials, plan)
    show_event(materials, sess_y.play('cinder-a', 'bench', need_a[0], need_a[1],
                                      note='分支乙：完整播完'))
    nxt_y = sess_y.next_segment()
    print_next(sess_y, prefix='    分支乙下一段：')

    assert nxt_x['pair'] == 'P1' and nxt_x['side'] == 'a' and nxt_x['window'] == (1800, 1960)
    assert nxt_y['pair'] == 'P1' and nxt_y['side'] == 'b' and nxt_y['reel'] == 'vireo-b'
    assert (nxt_x['reel'], nxt_x['window']) != (nxt_y['reel'], nxt_y['window'])
    print('  → 同一组材料、同一份清单，叫停位置不同，下一段该听的就不同。')

    print('\n== 正式会议：从会前状态重新开始 ==')
    session = Session(materials, plan)
    assert all(session.pair_state(p) == 'open' for p in plan.pairs)
    print('  会前检查：3 对全部未开始 —— 候选清单本身不等于已听记录。')

    def play_and_next(*args, **kwargs):
        event = session.play(*args, **kwargs)
        show_event(materials, event)
        print_next(session)
        return event

    p1, p2, p3 = plan.pairs

    print('— P1.A：先只播目标行，漏了前后文 —')
    play_and_next('cinder-a', 'bench', 1700, 1900, note='只播目标行')
    play_and_next('cinder-a', 'bench', 1640, 1700, note='补前文')
    play_and_next('cinder-a', 'bench', 1900, 1960, note='补后文')
    assert not session.side_missing(p1.a)

    print('— P1.B：中间跳过一小段 —')
    play_and_next('vireo-b', 'soft', 2420, 2740, skip=[(2600, 2640)], note='跳过一小段')
    assert session.side_missing(p1.b) == [(2600, 2640)]
    play_and_next('vireo-b', 'soft', 2580, 2660, note='补听跳过处，带宽余')
    assert session.pair_state(p1) == 'complete'
    assert not session.side_missing(p2.a)  # 刚才播到的可读行同时满足了 P2.A
    print('    → P2.A 已被刚才的播放满足，下一轮只需补 P2.B。')

    print('— P2.B：跳到末尾，再换标定重听，都不算 —')
    ev = play_and_next('cinder-a', 'bench', 6140, 6460,
                       stop_at=6250, ended='jumped', note='跳到末尾')
    assert ev['heard'] == [(6140, 6250)] and ev['unplayed'] == [(6250, 6460)]
    assert session.side_missing(p2.b) == [(6250, 6460)]
    play_and_next('cinder-a', 'soft', 6250, 6460, note='换标定重听同一画格')
    assert session.evidence[('cinder-a', 'soft')] == [(6250, 6460)]  # 记在了 soft 名下
    assert session.side_missing(p2.b) == [(6250, 6460)]              # bench 侧仍缺
    play_and_next('cinder-a', 'bench', 6250, 6460, note='换回原标定补听')
    assert session.pair_state(p2) == 'complete'

    print('— 重听已听过的段落：允许，但不改变证据 —')
    before = dict(session.evidence)
    play_and_next('cinder-a', 'bench', 1700, 1900, note='重听确认 P1.A')
    assert session.evidence == before and session.pair_state(p1) == 'complete'

    print('— P3.A：播放窗口经过缺页，补零不算 —')
    ev = play_and_next('cinder-a', 'bench', 5550, 5950, note='按计划窗口播放')
    assert ev['heard'] == [(5770, 5950)] and ev['zeroed'] == [(5550, 5770)]
    assert session.side_missing(p3.a) == [(5550, 5770)]

    print('— P3.B：正常补听 —')
    play_and_next('vireo-b', 'bench', 850, 1150)
    nxt = session.next_segment()
    assert nxt['pair'] is None and nxt['blocked']

    print('\n== 会后复查报告 ==')
    print(session.report())
    assert session.pair_state(p1) == session.pair_state(p2) == 'complete'
    assert session.pair_state(p3) == 'half'
    assert len(session.events) == 11
    print('\n演示通过：叫停位置决定下一段；跳过、跳到末尾、补零、换标定都填不上证据；'
          '报告只来自播放事件。')


# ---------------------------------------------------------------- 现场 CLI

def parse_span(text):
    s, e = text.split(':')
    return (int(s), int(e))


def load_session(materials, plan, path):
    session = Session(materials, plan)
    if path.exists():
        data = json.loads(path.read_text(encoding='utf-8'))
        for ev in data['events']:
            session.play(ev['reel'], ev['profile'], ev['start'], ev['end'],
                         stop_at=ev.get('stop_at'), skip=[tuple(x) for x in ev.get('skip', [])],
                         ended=ev.get('ended'), note=ev.get('note', ''))
    return session


def save_session(session, path):
    events = []
    for ev in session.events:
        events.append({'reel': ev['reel'], 'profile': ev['profile'],
                       'start': ev['window'][0], 'end': ev['window'][1],
                       'stop_at': None if ev['ended'] == 'completed' else ev['stop_at'],
                       'skip': [list(s) for s in ev['skip']],
                       'ended': ev['ended'], 'note': ev['note']})
    path.write_text(json.dumps({'events': events}, ensure_ascii=False, indent=2) + '\n',
                    encoding='utf-8')


def main():
    cli = argparse.ArgumentParser(description=__doc__,
                                  formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = cli.add_subparsers(dest='command', required=True)
    sub.add_parser('demo', help='用样例材料演示完整流程')
    p_init = sub.add_parser('init', help='写出可编辑的材料与候选清单样例')
    p_init.add_argument('--dir', required=True)
    for name in ('plan', 'next', 'play', 'report'):
        p = sub.add_parser(name)
        p.add_argument('--materials', required=True)
        p.add_argument('--plan', required=True)
        if name != 'plan':
            p.add_argument('--session', required=True)
        if name == 'play':
            p.add_argument('--reel', required=True)
            p.add_argument('--profile', required=True)
            p.add_argument('--start', type=int, required=True)
            p.add_argument('--end', type=int, required=True)
            p.add_argument('--stop-at', type=int)
            p.add_argument('--ended', choices=list(ENDED_CN))
            p.add_argument('--skip', action='append', default=[], metavar='S:E')
            p.add_argument('--note', default='')
    args = cli.parse_args()

    if args.command == 'demo':
        demo()
        return
    if args.command == 'init':
        root = Path(args.dir)
        root.mkdir(parents=True, exist_ok=True)
        for name, data in (('materials.json', DEMO_MATERIALS), ('plan.json', DEMO_PLAN)):
            (root / name).write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n',
                                     encoding='utf-8')
        print(f'已写出 {root}/materials.json 与 {root}/plan.json；'
              'session.json 会在首次 play 后生成')
        return

    materials = Materials.from_dict(json.loads(Path(args.materials).read_text(encoding='utf-8')))
    plan = Plan.from_dict(json.loads(Path(args.plan).read_text(encoding='utf-8')), materials)
    if args.command == 'plan':
        print(plan.candidates_text(materials))
        return
    session_path = Path(args.session)
    session = load_session(materials, plan, session_path)
    if args.command == 'next':
        print_next(session)
    elif args.command == 'report':
        print(session.report())
    elif args.command == 'play':
        event = session.play(args.reel, args.profile, args.start, args.end,
                             stop_at=args.stop_at, ended=args.ended,
                             skip=[parse_span(x) for x in args.skip], note=args.note)
        save_session(session, session_path)
        show_event(materials, event)
        print_next(session)


if __name__ == '__main__':
    main()
