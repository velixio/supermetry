export function rampStep(value: number, maximum: number): 0 | 1 | 2 | 3 | 4 | 5 {
  if (!Number.isFinite(value) || !Number.isFinite(maximum) || value <= 0 || maximum <= 0) return 0
  return Math.min(5, Math.max(1, Math.ceil((value / maximum) * 5))) as 1 | 2 | 3 | 4 | 5
}
