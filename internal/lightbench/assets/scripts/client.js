export function captureURL(reel, action, params) {
  return `/captures/${encodeURIComponent(reel)}/${action}?${new URLSearchParams(params)}`;
}

export function benchURL(reel, action, params) {
  return `/lightbench/${encodeURIComponent(reel)}/${action}?${new URLSearchParams(params)}`;
}

export async function request(url, options) {
  const response = await fetch(url, options);
  if (!response.ok) {
    const body = response.status === 503 ? { error: '工位繁忙，请稍后重试。' } : await response.json().catch(() => null);
    throw new Error(/[\u3400-\u9fff]/.test(body?.error ?? '') ? body.error : '未能完成请求，请检查输入后重试。');
  }
  return response.json();
}

export const statusName = Object.freeze({ readable: '可读', missing_sheet: '声槽缺页', outside_capture: '移出卷界' });
export const position = (absolute, rows) => `${Math.floor(absolute / rows)} 格 ${absolute % rows} 行`;
export const compactNumber = (value) => new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value);
