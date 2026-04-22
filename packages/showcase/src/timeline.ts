/**
 * Single source of truth for scene frame timestamps.
 * One scene only — 33 seconds @ 30fps.
 */

export const FPS = 30;

export const SCENES = {
  deploy: { start: 0, duration: 990 },  // 0:00 – 0:33
} as const;

export type SceneName = keyof typeof SCENES;

export const TOTAL_FRAMES = SCENES.deploy.duration; // 990

export function relFrame(scene: SceneName, absFrame: number): number {
  return absFrame - SCENES[scene].start;
}

export function easeInOut(t: number): number {
  return t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t;
}

export function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}
