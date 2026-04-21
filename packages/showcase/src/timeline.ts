/**
 * Single source of truth for scene frame timestamps.
 * 2 scenes, ~40 seconds total @ 30fps
 */

export const FPS = 30;

export const SCENES = {
  deploy: { start: 0,    duration: 1020 },  // 0:00 – 0:34  (34s)
  outro:  { start: 1020, duration: 270 },   // 0:34 – 0:43  (9s)
} as const;

export type SceneName = keyof typeof SCENES;

export const TOTAL_FRAMES = SCENES.outro.start + SCENES.outro.duration; // 1290 = 0:43

export function relFrame(scene: SceneName, absFrame: number): number {
  return absFrame - SCENES[scene].start;
}

export function easeInOut(t: number): number {
  return t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t;
}

export function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}
