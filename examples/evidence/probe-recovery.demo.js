// Readable demonstration of evidence-based probe recovery.
//
// Run with: node examples/evidence/probe-recovery.demo.js
//
// It replays the two situations from the work order against the real ledger:
//
//   1. one picture frame inspected under two calibrations, including an old
//      profile answer that arrives late;
//   2. one positioning across the missing picture page, where the sound slot
//      comes from a different, offset page and stays audible.
//
// At each step it prints which evidence is verified, which must be re-fetched,
// and which material simply does not exist. A late old answer is shown being
// rejected so old sound is never paired with a new picture or calibration.

import {
  ABSENT, PENDING, VERIFIED, PICTURE, MAPPING, READING,
  PIECE_LABELS, STATE_LABELS, createProbeLedger,
} from '../../internal/lightbench/assets/scripts/evidence.js';
import { FakeTransport, GAP, windowResponse } from './fake-transport.js';

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const order = [PICTURE, MAPPING, READING];

function snapshotLine(ledger) {
  return order.map((id) => {
    const piece = ledger.piece(id);
    const state = piece.state ? STATE_LABELS[piece.state] : '未开始';
    return `${PIECE_LABELS[id]}=${state}`;
  }).join(' · ');
}

async function wait(ledger) {
  while (order.some((id) => [PENDING, null].includes(ledger.piece(id).state))) await tick();
}

function heading(text) {
  console.log(`\n${'='.repeat(72)}\n${text}\n${'='.repeat(72)}`);
}

async function scenarioTwoCalibrations() {
  heading('场景一：同一画格的两次标定，旧标定的迟到读数不得配到新标定上');
  const transport = new FakeTransport();
  const ledger = createProbeLedger(transport);
  transport.holdKinds.add(READING);

  ledger.setWindow(windowResponse('bench'));
  ledger.select(0, 400);
  await tick();
  console.log('① bench 标定下定位画格 0 行 400，读数请求挂起：');
  console.log('   ' + snapshotLine(ledger));
  const oldCall = transport.held.find((call) => call.kind === READING);
  console.log(`   挂起请求的标定 = ${oldCall.params.profile}，键 = ${oldCall.params.key}`);

  ledger.setWindow(windowResponse('soft'));
  ledger.select(0, 400);
  transport.holdKinds.delete(READING);
  await tick();
  console.log('\n② 改用 soft 标定，画面与定位键不变，旧读数退役并按新标定重取：');
  console.log('   ' + snapshotLine(ledger));
  const softCall = transport.last(READING);
  console.log(`   新读数请求的标定 = ${softCall.params.profile}，键 = ${softCall.params.key}`);

  oldCall.resolve({
    status: 'readable', source: { frame: 0, row: 630 },
    gray: [6000, 8200], sample: [99999, -99999], clipped: [true, true],
  });
  await tick();
  console.log('\n③ bench 的旧读数姗姗来迟（采样 99999），键已不匹配，被丢弃：');
  console.log('   ' + snapshotLine(ledger));
  console.log(`   当前读数仍是 ${STATE_LABELS[ledger.piece(READING).state]}，未被 99999 污染。`);

  softCall.resolve({
    status: 'readable', source: { frame: 0, row: 630 },
    gray: [12000, 14000], sample: [4630, -4630], clipped: [false, false],
  });
  await wait(ledger);
  console.log('\n④ soft 读数返回后，三项证据归属当前标定：');
  console.log('   ' + snapshotLine(ledger));
  console.log(`   当前左采样 = ${ledger.piece(READING).data.sample[0]}（不是旧值 99999）。`);
}

async function scenarioCrossGap() {
  heading('场景二：跨缺页定位——画面页本来没有，偏移后的声槽仍可读');
  const transport = new FakeTransport();
  const ledger = createProbeLedger(transport);
  ledger.setWindow(windowResponse('bench'));

  ledger.select(GAP, 1800);
  await wait(ledger);
  console.log(`① 定位缺页画格 ${GAP} 行 1800（该画格没有画面扫描页）：`);
  console.log('   ' + snapshotLine(ledger));
  const mapping = ledger.piece(MAPPING);
  console.log(`   声槽实际来自源页 ${mapping.data.source.frame} 行 ${mapping.data.source.row}，状态 ${mapping.data.status}。`);
  console.log(`   画面=${STATE_LABELS[ABSENT]}属于材料本来没有，不产生请求也不会重试。`);

  // Show that retry does not touch absent material or verified evidence.
  const before = { picture: transport.count(PICTURE), mapping: transport.count(MAPPING), reading: transport.count(READING) };
  ledger.retry();
  await tick();
  const after = { picture: transport.count(PICTURE), mapping: transport.count(MAPPING), reading: transport.count(READING) };
  console.log('\n② 对该位置执行「只重试待取」：');
  console.log(`   请求数 前/后 画面 ${before.picture}/${after.picture}，定位 ${before.mapping}/${after.mapping}，读数 ${before.reading}/${after.reading}（无变化）。`);

  // A failed picture on an existing frame is recoverable without touching sound.
  heading('补充分支：画面请求失败时，已核实的声音证据保留，只重取画面');
  transport.fail.add(PICTURE);
  ledger.select(0, 400);
  await wait(ledger);
  console.log('③ 画格 0 的画面请求失败，定位与读数仍已核实：');
  console.log('   ' + snapshotLine(ledger));
  ledger.retry([PICTURE]);
  await wait(ledger);
  console.log('   只重取画面后：' + snapshotLine(ledger));
}

async function scenarioRetryOnlyPending() {
  heading('补充分支：局部失败时只重试待取的那一部分');
  const transport = new FakeTransport();
  const ledger = createProbeLedger(transport);
  ledger.setWindow(windowResponse('bench'));
  transport.fail.add(READING);
  ledger.select(0, 400);
  await wait(ledger);
  console.log('① 读数请求失败，画面与定位保持已核实：');
  console.log('   ' + snapshotLine(ledger));
  const calls = transport.count(MAPPING);
  ledger.retry([READING]);
  await wait(ledger);
  console.log('② 只重取读数，定位请求数不变（' + calls + ' -> ' + transport.count(MAPPING) + '）：');
  console.log('   ' + snapshotLine(ledger));
  console.log(`   读数恢复为 ${STATE_LABELS[VERIFIED]}，左采样 = ${ledger.piece(READING).data.sample[0]}。`);
}

console.log('Emberstripe 探针证据恢复演示（使用与工作台相同的证据账本）');
await scenarioTwoCalibrations();
await scenarioCrossGap();
await scenarioRetryOnlyPending();
console.log('\n演示结束：迟到旧证据一律按键丢弃，材料缺失与读取失败分别标注，重试只取待取部分。');
