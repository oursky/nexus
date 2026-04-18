/**
 * Single source of truth for scene frame timestamps.
 * All scenes reference this file for their start frame and duration.
 * Total: 6150 frames @ 30fps = 205 seconds
 */

export const FPS = 30;

export const SCENES = {
  intro:          { start: 0,    duration: 300 },  // 0:00 – 0:10  (10s)
  problem:        { start: 300,  duration: 750 },  // 0:10 – 0:35  (25s)
  architecture:   { start: 1050, duration: 600 },  // 0:35 – 0:55  (20s)
  deploy:         { start: 1650, duration: 900 },  // 0:55 – 1:25  (30s)
  macosConnect:   { start: 2550, duration: 900 },  // 1:25 – 1:55  (30s)
  createWorkspace:{ start: 3450, duration: 750 },  // 1:55 – 2:20  (25s)
  livePreview:    { start: 4200, duration: 750 },  // 2:20 – 2:45  (25s)
  workspaceTypes: { start: 4950, duration: 750 },  // 2:45 – 3:10  (25s)
  outro:          { start: 5700, duration: 450 },  // 3:10 – 3:25  (15s)
} as const;

export type SceneName = keyof typeof SCENES;

/** Total composition duration in frames */
export const TOTAL_FRAMES =
  SCENES.outro.start + SCENES.outro.duration; // 6150

/** Helper: get frame number relative to a scene's start */
export function relFrame(scene: SceneName, absFrame: number): number {
  return absFrame - SCENES[scene].start;
}

/** Easing helpers */
export function easeInOut(t: number): number {
  return t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t;
}

export function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}
