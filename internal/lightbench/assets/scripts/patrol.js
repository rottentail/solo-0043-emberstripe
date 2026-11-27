import { benchURL, request } from './client.js';

const verdictName = Object.freeze({ healthy: '可听清', trend_only: '仅可追踪趋势', failed: '判断不成立' });
const kindName = Object.freeze({ static: '静态背景', modulated: '确定调制' });
const ruleName = Object.freeze({
  short_content: '有效内容过短',
  background_step: '背景出现台阶',
  lane_plateau: '声槽被压成平台',
  clipped_platform: '削顶平台',
});
const laneName = ['左声槽', '右声槽'];

export function patrol(root, onReview) {
	const button = root.querySelector('#patrol-run');
	const status = root.querySelector('#patrol-status');
	const report = root.querySelector('#patrol-report');
	let windowData;

	const statusClass = (verdict) => `status-${verdict.replace('_', '-')}`;

	function findingRow(finding) {
		const row = document.createElement('li');
		row.className = `finding severity-${finding.severity}`;
		const link = document.createElement('button');
		link.type = 'button';
		link.className = 'finding-link';
		link.textContent = `复查 ${finding.frame} 格 ${finding.row} 行`;
		link.addEventListener('click', () => onReview(finding.review_window.start, finding.review_window.end, finding.frame, finding.row));
		const text = document.createElement('span');
		text.textContent = `${ruleName[finding.rule] ?? finding.rule} · ${finding.readable_rows} 行可读 · ${finding.detail}`;
		row.append(link, text);
		return row;
	}

	function laneBlock(lane, data) {
		const block = document.createElement('div');
		block.className = `lane-block ${statusClass(data.verdict)}`;
		const head = document.createElement('p');
		head.className = 'lane-head';
		head.innerHTML = `<strong>${laneName[lane]}</strong><span>${verdictName[data.verdict] ?? data.verdict}</span>`;
		const metrics = document.createElement('p');
		metrics.className = 'lane-metrics muted';
		metrics.textContent = `可读 ${data.readable_rows} 行 · 电平 ${data.distinct_levels} 种 · 变化占比 ${(data.changed_ratio * 100).toFixed(1)}% · 削顶 ${data.clipped_rows} 行`;
		block.append(head, metrics);
		if (data.findings.length) {
			const list = document.createElement('ul');
			list.className = 'finding-list';
			list.append(...data.findings.map(findingRow));
			block.append(list);
		}
		return block;
	}

	function segmentBlock(segment) {
		const block = document.createElement('article');
		block.className = `segment ${statusClass(segment.verdict)}`;
		const head = document.createElement('div');
		head.className = 'segment-head';
		const title = document.createElement('h3');
		title.textContent = `${segment.segment.id} · ${kindName[segment.segment.kind] ?? segment.segment.kind}`;
		const badge = document.createElement('span');
		badge.className = `verdict ${statusClass(segment.verdict)}`;
		badge.textContent = verdictName[segment.verdict] ?? segment.verdict;
		const range = document.createElement('p');
		range.className = 'segment-range muted';
		range.textContent = `画格 ${segment.segment.start_frame}–${segment.segment.end_frame - 1}（半开）`;
		head.append(title, badge);
		block.append(head, range, laneBlock(0, segment.lanes[0]), laneBlock(1, segment.lanes[1]));
		return block;
	}

	async function run() {
		if (!windowData) return;
		button.disabled = true;
		status.dataset.error = '';
		status.textContent = '正在巡检标记片段…';
		try {
			const data = await request(benchURL(windowData.reel, 'patrol', { profile: windowData.profile.profile }));
			status.textContent = `标定 ${data.profile} · 总体结论：${verdictName[data.verdict] ?? data.verdict}`;
			report.hidden = false;
			report.replaceChildren(...data.segments.map(segmentBlock));
		} catch (error) {
			status.dataset.error = '';
			status.textContent = error.message;
		} finally {
			button.disabled = false;
		}
	}

	button.addEventListener('click', run);

	return {
		setWindow(data) {
			windowData = data;
			button.disabled = false;
		},
	};
}
