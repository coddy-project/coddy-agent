import type { Camera } from "./camera";

/** How long after the last move the board stops being promoted to a layer
 *  of its own (will-change), so the pages are drawn sharp again. */
export const SETTLE_MS = 160;

/**
 * The camera of one board, outside React: a pan or a zoom writes the
 * transform of the board's world layer and the scale its counter-scaled parts
 * read (--canvas-scale, --canvas-inv) once per animation frame, and renders
 * nothing. What shows the zoom (the level of the zoom controls) subscribes.
 */
export class CameraStore {
  private camera: Camera = { x: 0, y: 0, scale: 1 };
  private fit = true;
  private world: HTMLElement | null = null;
  private board: HTMLElement | null = null;
  private frame = 0;
  private scheduled = false;
  private settle = 0;
  private readonly listeners = new Set<() => void>();

  get(): Camera {
    return this.camera;
  }

  /** Whether the camera is the one Fit gave, untouched since. */
  fitted(): boolean {
    return this.fit;
  }

  /** The level the zoom controls show: null while fitted. */
  percent(): number | null {
    return this.fit ? null : Math.round(this.camera.scale * 100);
  }

  /** Where the camera writes: the world layer and the board around it. */
  attach(world: HTMLElement | null, board: HTMLElement | null): void {
    this.world = world;
    this.board = board;
    this.write();
  }

  /** A move the person made (a pan, a zoom): the board stops following Fit. */
  move(next: Camera | ((c: Camera) => Camera)): void {
    this.apply(typeof next === "function" ? next(this.camera) : next, false);
  }

  /** The camera Fit gave. */
  fitTo(next: Camera): void {
    this.apply(next, true);
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  dispose(): void {
    if (this.frame) cancelAnimationFrame(this.frame);
    if (this.settle) window.clearTimeout(this.settle);
    this.frame = 0;
    this.scheduled = false;
    this.settle = 0;
    this.listeners.clear();
    this.world = null;
    this.board = null;
  }

  private apply(next: Camera, fit: boolean): void {
    const same =
      next.x === this.camera.x &&
      next.y === this.camera.y &&
      next.scale === this.camera.scale &&
      fit === this.fit;
    if (same) return;
    this.camera = next;
    this.fit = fit;
    if (!this.scheduled) {
      this.scheduled = true;
      this.frame = requestAnimationFrame(() => {
        this.scheduled = false;
        this.frame = 0;
        this.write();
        for (const listener of [...this.listeners]) listener();
      });
    }
    if (!fit) this.moving();
  }

  private write(): void {
    const { x, y, scale } = this.camera;
    if (this.world) {
      this.world.style.transform = `translate(${x}px, ${y}px) scale(${scale})`;
    }
    if (this.board) {
      this.board.style.setProperty("--canvas-scale", String(scale));
      this.board.style.setProperty("--canvas-inv", String(1 / scale));
    }
  }

  // While the person moves the board it is one layer the compositor moves;
  // once it rests, the pages are drawn at the new scale.
  private moving(): void {
    this.board?.classList.add("is-moving");
    if (this.settle) window.clearTimeout(this.settle);
    this.settle = window.setTimeout(() => {
      this.settle = 0;
      this.board?.classList.remove("is-moving");
    }, SETTLE_MS);
  }
}
