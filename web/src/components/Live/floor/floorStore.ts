import type { FloorModel, FloorPulse } from './floorModel';

// ── What the floor remembers between visits ──────────────────────────────
//
// The Live page unmounts when the user goes to the Board and mounts again
// when they come back, and the floor used to come back blank: no pulses in
// the feed, the camera at home, follow and spin off, and the first floor it
// built diffed against nothing — so nothing that happened while they were
// away was there to be seen. The SELECTION lives in the URL (see LiveView);
// everything else the scene owns lives here, at module level, for the life
// of the tab, and the few scalars worth keeping across a reload are mirrored
// to sessionStorage.

export interface FloorCamera {
  position: [number, number, number];
  target: [number, number, number];
}

interface FloorMemory {
  /** The changes still worth flashing and listing. */
  pulses: FloorPulse[];
  /** The last floor the pulses were diffed against. */
  prevFloor: FloorModel | null;
  /** Where the user left the camera; null means home. */
  camera: FloorCamera | null;
  follow: boolean;
  autoRotate: boolean;
}

const KEY = 'slmcode:floor';

interface Stored {
  camera?: FloorCamera | null;
  follow?: boolean;
  autoRotate?: boolean;
}

function readStored(): Stored {
  try {
    const raw = sessionStorage.getItem(KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Stored;
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

const stored = readStored();

export const floorStore: FloorMemory = {
  pulses: [],
  prevFloor: null,
  camera: isCamera(stored.camera) ? stored.camera : null,
  follow: stored.follow === true,
  autoRotate: stored.autoRotate === true,
};

function isCamera(c: unknown): c is FloorCamera {
  if (!c || typeof c !== 'object') return false;
  const { position, target } = c as FloorCamera;
  const ok = (v: unknown) => Array.isArray(v) && v.length === 3 && v.every((n) => typeof n === 'number' && Number.isFinite(n));
  return ok(position) && ok(target);
}

/** Persist the scalars worth keeping across a reload. */
export function persistFloorStore(): void {
  try {
    const out: Stored = { camera: floorStore.camera, follow: floorStore.follow, autoRotate: floorStore.autoRotate };
    sessionStorage.setItem(KEY, JSON.stringify(out));
  } catch {
    /* private mode — the in-memory copy still serves this tab */
  }
}

export function rememberCamera(camera: FloorCamera | null): void {
  floorStore.camera = camera;
  persistFloorStore();
}

export function rememberCameraPrefs(prefs: { follow?: boolean; autoRotate?: boolean }): void {
  if (prefs.follow !== undefined) floorStore.follow = prefs.follow;
  if (prefs.autoRotate !== undefined) floorStore.autoRotate = prefs.autoRotate;
  persistFloorStore();
}

/** Forget everything — tests, and a new tab's first floor. */
export function resetFloorStore(): void {
  floorStore.pulses = [];
  floorStore.prevFloor = null;
  floorStore.camera = null;
  floorStore.follow = false;
  floorStore.autoRotate = false;
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}
