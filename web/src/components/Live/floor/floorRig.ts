import { useRef, type RefObject } from 'react';
import * as THREE from 'three';
import type { Mood } from './floorLife';

// ── A person, with limbs ─────────────────────────────────────────────────
//
// Everyone on the 3D floor is the same little figure: legs, a torso, arms on
// shoulder pivots and a head. The pose is set per frame by poseRig from the
// person's mood — typing, sipping a coffee, stretching, walking, dozing with
// their head on the desk, raising a beer, dancing — so a scene of twenty
// people costs twenty sets of transforms and no re-renders.
//
// Local space: the figure faces −z, its feet are at y = 0. Seated, it drops
// onto a chair and its legs swing forward under the desk.

export interface Rig {
  /** Moves and turns the whole figure: walks, standing up. */
  root: RefObject<THREE.Group | null>;
  /** Bobs and leans the upper body. */
  body: RefObject<THREE.Group | null>;
  head: RefObject<THREE.Group | null>;
  armL: RefObject<THREE.Group | null>;
  armR: RefObject<THREE.Group | null>;
  legL: RefObject<THREE.Group | null>;
  legR: RefObject<THREE.Group | null>;
  beer: RefObject<THREE.Group | null>;
  cup: RefObject<THREE.Mesh | null>;
}

export function useRig(): Rig {
  return {
    root: useRef<THREE.Group>(null),
    body: useRef<THREE.Group>(null),
    head: useRef<THREE.Group>(null),
    armL: useRef<THREE.Group>(null),
    armR: useRef<THREE.Group>(null),
    legL: useRef<THREE.Group>(null),
    legR: useRef<THREE.Group>(null),
    beer: useRef<THREE.Group>(null),
    cup: useRef<THREE.Mesh>(null),
  };
}

/** How far the body drops when seated. */
export const SIT_DROP = 0.2;
export const HIP_Y = 0.42;
export const SHOULDER_Y = 1.1;
export const HEAD_Y = 1.38;

export interface RigLook {
  shirt: string;
  skin: string;
  hair?: string;
  /** Half-transparent: a seat the pipeline lent, or someone who has not spoken yet. */
  ghost?: boolean;
  crown?: boolean;
}

export interface PoseInputs {
  mood: Mood;
  /** Elapsed seconds (the scene clock). */
  t: number;
  /** Seconds since this mood began. */
  since: number;
  /** A per-person phase, so a table does not move in lockstep. */
  phase: number;
  /** 0 = standing, 1 = seated; eased by the caller. */
  sit: number;
  /** Walking speed right now, units per second (0 when still). */
  speed: number;
  /** The head's glance, when watching. */
  glance: number;
  /** Working by talking (on the command center's pad), not by typing. */
  talk?: boolean;
}

const lerp = THREE.MathUtils.lerp;

/** Eases a rotation toward a goal: poses blend rather than snap. */
function ease(obj: THREE.Object3D | null, axis: 'x' | 'y' | 'z', goal: number, k = 0.18) {
  if (!obj) return;
  obj.rotation[axis] = lerp(obj.rotation[axis], goal, k);
}

/**
 * poseRig sets every joint for this frame. Rotations about x swing a limb
 * forward (toward −z) for positive angles: an arm at 1.3 types, at π points
 * straight up.
 */
export function poseRig(rig: Rig, p: PoseInputs): void {
  const { mood, t, since, phase, sit, speed } = p;
  const walking = speed > 0.05;
  const stride = walking ? Math.sin(t * 9 + phase) * 0.55 : 0;
  let armL = 0.25;
  let armR = 0.25;
  let armLz = 0;
  let armRz = 0;
  let headX = 0;
  let headY = p.glance;
  let bodyX = 0;
  let bodyY = 0;
  let bodyZ = 0;
  let bob = 0;
  let kick = 0;
  let beer = false;
  let cup = false;

  switch (mood) {
    case 'work': {
      if (p.talk) {
        // Holding forth: hands that talk, a nod on the point.
        armL = 0.7 + Math.sin(t * 2.6 + phase) * 0.45;
        armR = 1.3 + Math.sin(t * 3.4 + phase + 1) * 0.55;
        armLz = -0.25;
        armRz = 0.3;
        headX = Math.sin(t * 4) * 0.08;
        bob = Math.abs(Math.sin(t * 2.6)) * 0.03;
        headY = Math.sin(t * 0.9) * 0.3;
        break;
      }
      // Typing: both hands on the keys, a nod in time.
      armL = 1.25 + Math.sin(t * 16 + phase) * 0.1;
      armR = 1.25 + Math.sin(t * 16 + phase + 1.7) * 0.1;
      bodyX = -0.06 + Math.sin(t * 9) * 0.03;
      bob = Math.abs(Math.sin(t * 9)) * 0.02;
      headY = 0;
      break;
    }
    case 'watch': {
      armL = sit > 0.5 ? 0.9 : 0.2;
      armR = sit > 0.5 ? 0.9 : 0.2;
      bob = Math.sin(t * 1.2 + phase) * 0.008;
      break;
    }
    case 'coffee': {
      // A sip every four seconds, the cup back down between.
      cup = true;
      const k = ((since + phase) % 4) / 4;
      const sip = k > 0.55 && k < 0.85 ? Math.sin(((k - 0.55) / 0.3) * Math.PI) : 0;
      armR = 0.95 + sip * 1.35;
      armRz = sip * 0.45;
      headX = sip * 0.25;
      armL = sit > 0.5 ? 0.9 : 0.25;
      bob = Math.sin(t * 1.2 + phase) * 0.008;
      break;
    }
    case 'stretch': {
      // Arms up, a lean one way then the other, a yawn of a head tilt.
      const s = Math.sin(since * 1.4);
      armL = Math.PI - 0.15;
      armR = Math.PI - 0.15;
      armLz = -0.25 - s * 0.15;
      armRz = 0.25 - s * 0.15;
      bodyZ = s * 0.12;
      headX = 0.25;
      bob = 0.02;
      break;
    }
    case 'stroll': {
      armL = 0.1;
      armR = 0.1;
      headY = walking ? 0 : Math.sin(t * 0.8 + phase) * 0.6;
      break;
    }
    case 'nap': {
      // Head down on folded arms, a slow breath.
      armL = sit > 0.5 ? 1.35 : 0.35;
      armR = sit > 0.5 ? 1.35 : 0.35;
      armLz = sit > 0.5 ? 0.5 : 0;
      armRz = sit > 0.5 ? -0.5 : 0;
      bodyX = sit > 0.5 ? -0.42 : -0.1;
      headX = -0.55;
      headY = 0.35;
      bob = Math.sin(t * 1.1 + phase) * 0.012;
      break;
    }
    case 'cheers': {
      // Beer up, a clink toward the middle every few seconds.
      beer = true;
      const clink = Math.max(0, Math.sin(t * 2.2 + phase)) ** 6;
      armR = 2.3 + clink * 0.5;
      armRz = 0.2;
      armL = 0.3 + Math.sin(t * 3 + phase) * 0.15;
      bob = Math.abs(Math.sin(t * 3 + phase)) * 0.04;
      headX = 0.2;
      break;
    }
    case 'football': {
      // On the toes, arms out for balance, the odd kick.
      armL = 0.5;
      armR = 0.5;
      armLz = -0.5;
      armRz = 0.5;
      bob = Math.abs(Math.sin(t * 6 + phase)) * 0.06;
      kick = Math.max(0, Math.sin(t * 1.6 + phase)) ** 8 * 1.1;
      break;
    }
    case 'dance': {
      const beat = t * 5 + phase;
      armL = Math.PI * 0.75 + Math.sin(beat) * 0.6;
      armR = Math.PI * 0.75 - Math.sin(beat) * 0.6;
      armLz = -0.4;
      armRz = 0.4;
      bodyY = Math.sin(beat * 0.5) * 0.5;
      bodyZ = Math.sin(beat) * 0.12;
      bob = Math.abs(Math.sin(beat)) * 0.09;
      break;
    }
  }

  if (walking) {
    // Arms swing against the legs; nothing else carries on mid-stride.
    armL = 0.1 - stride * 0.8;
    armR = 0.1 + stride * 0.8;
    armLz = 0;
    armRz = 0;
    bob = Math.abs(Math.sin(t * 9 + phase)) * 0.05;
    bodyX = 0.05;
    bodyY = 0;
    beer = mood === 'cheers';
  }

  if (rig.body.current) {
    rig.body.current.position.y = bob - sit * SIT_DROP;
  }
  ease(rig.body.current, 'x', bodyX);
  ease(rig.body.current, 'y', bodyY);
  ease(rig.body.current, 'z', bodyZ);
  ease(rig.head.current, 'x', headX, 0.1);
  ease(rig.head.current, 'y', headY, 0.06);
  ease(rig.armL.current, 'x', armL, 0.22);
  ease(rig.armR.current, 'x', armR, 0.22);
  ease(rig.armL.current, 'z', armLz, 0.22);
  ease(rig.armR.current, 'z', armRz, 0.22);
  // Seated, the thighs swing forward under the desk; standing, they walk.
  const legSit = sit * 1.45;
  if (rig.legL.current) {
    rig.legL.current.rotation.x = lerp(rig.legL.current.rotation.x, legSit + stride, 0.3);
    rig.legL.current.position.y = HIP_Y - sit * SIT_DROP;
  }
  if (rig.legR.current) {
    rig.legR.current.rotation.x = lerp(rig.legR.current.rotation.x, legSit - stride + kick, 0.3);
    rig.legR.current.position.y = HIP_Y - sit * SIT_DROP;
  }
  if (rig.beer.current) rig.beer.current.visible = beer;
  if (rig.cup.current) rig.cup.current.visible = cup && !walking;
}

/**
 * Pose a rig without animation (prefers-reduced-motion): a still picture of
 * the same mood.
 */
export function stillPose(rig: Rig, mood: Mood, sit: number, glance: number, talk = false): void {
  for (let i = 0; i < 30; i++) poseRig(rig, { mood, t: 0.3, since: 2.5, phase: 0, sit, speed: 0, glance, talk });
}

const SKINS = ['#f1c27d', '#e0ac69', '#c68642', '#8d5524', '#ffdbac', '#f5d0b5'];
const HAIRS = ['#2b1d14', '#4a3222', '#a0522d', '#d6b370', '#111827', '#9ca3af', '#7c2d12'];
const SHIRTS = ['#c7d2fe', '#bae6fd', '#bbf7d0', '#fde68a', '#fecdd3', '#e9d5ff', '#fed7aa', '#99f6e4'];

/** A person's own look, from their seed: skin, hair and a shirt nobody else at the table shares often. */
export function lookOf(seed: number): { skin: string; hair: string; shirt: string } {
  return { skin: SKINS[seed % SKINS.length], hair: HAIRS[(seed >>> 4) % HAIRS.length], shirt: SHIRTS[(seed >>> 9) % SHIRTS.length] };
}

// ── Walking ──────────────────────────────────────────────────────────────

export interface Walk {
  points: THREE.Vector3[];
  /** Cumulative length at each point. */
  at: number[];
  total: number;
  /** Scene-clock seconds when the walk began. */
  start: number;
  speed: number;
}

export function makeWalk(points: THREE.Vector3[], start: number, speed = 1.6): Walk | null {
  const pts = points.filter((p, i) => i === 0 || p.distanceToSquared(points[i - 1]) > 1e-6);
  if (pts.length < 2) return null;
  const at = [0];
  for (let i = 1; i < pts.length; i++) at.push(at[i - 1] + pts[i].distanceTo(pts[i - 1]));
  return { points: pts, at, total: at[at.length - 1], start, speed };
}

/**
 * walkAt writes the position `t` seconds into a walk to `out`, and returns
 * the direction of travel's yaw (for a figure facing −z) or null when the
 * walk is over.
 */
export function walkAt(w: Walk, t: number, out: THREE.Vector3): number | null {
  const d = Math.max(0, (t - w.start) * w.speed);
  if (d >= w.total) {
    out.copy(w.points[w.points.length - 1]);
    return null;
  }
  let i = 1;
  while (i < w.points.length - 1 && w.at[i] < d) i++;
  const a = w.points[i - 1];
  const b = w.points[i];
  const seg = w.at[i] - w.at[i - 1] || 1;
  out.copy(a).lerp(b, (d - w.at[i - 1]) / seg);
  return Math.atan2(-(b.x - a.x), -(b.z - a.z));
}

/** Turns an angle toward a goal the short way round. */
export function turnToward(current: number, goal: number, k: number): number {
  let diff = goal - current;
  while (diff > Math.PI) diff -= Math.PI * 2;
  while (diff < -Math.PI) diff += Math.PI * 2;
  return current + diff * k;
}
