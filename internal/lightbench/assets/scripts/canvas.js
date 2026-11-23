export function context(canvas) {
  const width = canvas.clientWidth;
  const height = canvas.clientHeight;
  const scale = window.devicePixelRatio || 1;
  canvas.width = Math.round(width * scale);
  canvas.height = Math.round(height * scale);
  const paint = canvas.getContext('2d');
  paint.setTransform(scale, 0, 0, scale, 0, 0);
  return { paint, width, height };
}

export function fraction(event, element) {
  const { left, width } = element.getBoundingClientRect();
  return Math.max(0, Math.min(1, (event.clientX - left) / width));
}

export function vertical(paint, x, height, color, dashed = false) {
  paint.strokeStyle = color;
  paint.setLineDash(dashed ? [4, 4] : []);
  paint.beginPath();
  paint.moveTo(x, 0);
  paint.lineTo(x, height);
  paint.stroke();
  paint.setLineDash([]);
}
