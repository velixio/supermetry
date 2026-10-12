export type ReplaySize = { width: number; height: number }

export type ReplayFit = ReplaySize & {
  scale: number
  left: number
  top: number
}

export function fitReplayViewport(viewport: ReplaySize, stage: ReplaySize): ReplayFit {
  if (viewport.width <= 0 || viewport.height <= 0 || stage.width <= 0 || stage.height <= 0) {
    return { scale: 0, width: 0, height: 0, left: 0, top: 0 }
  }

  const scale = Math.min(stage.width / viewport.width, stage.height / viewport.height)
  const width = viewport.width * scale
  const height = viewport.height * scale
  return {
    scale,
    width,
    height,
    left: (stage.width - width) / 2,
    top: (stage.height - height) / 2,
  }
}
