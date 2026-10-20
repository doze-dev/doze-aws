import type { Page } from 'playwright';

/** A running doze-aws the runner booted. */
export interface Server {
  origin: string; // http://127.0.0.1:14960
  console: string; // http://127.0.0.1:14960/_console/
  account: string;
  region: string;
  /** The instance name the console prints in its top bar ("harbour"). */
  name: string;
}

/** What can be asked of one booted instance. */
export interface Inst {
  server: Server;
  /** The console URL of a path: `url('sqs/harbour-checkout-orders?tab=config')`. */
  url(path: string): string;
  /** An SQS JSON-1.0 call against the instance. */
  sqs(op: string, body: Record<string, unknown>): Promise<{ status: number; body: any }>;
  /** `http://host:port/<account>/<name>`: the URL SQS gives a queue. */
  queueUrl(name: string): string;
  /** `arn:aws:sqs:<region>:<account>:<name>`. */
  queueArn(name: string): string;
}

export interface Ctx extends Inst {
  page: Page;
  /** Put text into a CodeMirror-backed `textarea[data-editor]` the way the page's own code does. */
  setEditor(selector: string, value: string): Promise<void>;
  /** Wait for the page to stop moving (animation, htmx swap). */
  settle(ms?: number): Promise<void>;
  /**
   * Take a picture and the page's text now. Unlabelled, it is the page's
   * answer to the board (`page-<board>.png`); a driver that never takes one is
   * captured once, after `drive` returns. A board with several states (the
   * confirms board shows three dialogs) takes one unlabelled and a labelled
   * one per further state (`page-<board>-<label>.png`); the structural check
   * reads the text of all of them.
   */
  capture(label?: string): Promise<void>;
  /**
   * A second instance, booted lazily, for a board whose state the default one
   * cannot be in: `enforce` is IAM enforce mode, where a policy denies for real,
   * and is seeded with the same harbour fixture; `empty` has nothing in it at
   * all (the "none yet" boards); `fresh` is the harbour fixture again, for a
   * board whose state needs the real queues spent (a redrive that drains the
   * DLQ) and must not disturb the default instance; `map` and `mapLarge` are
   * wired as the Map and Map-Large boards draw (mapfixture.ts), with the
   * boards' flows hot; `wire` is the harbour fixture and then the afternoon the
   * wire boards show (e2e/fidelity/wire.ts: a bucket, a table, a function that
   * throws, a firing alarm, and the calls made against them, with the ring
   * started over before them); `wireEnforce` is that afternoon on an instance
   * that enforces IAM, with one more call: a read a bucket policy denies (the
   * Dark board). The page must be pointed at it with
   * `page.goto(inst.url(...))`.
   *
   * `reader` is the harbour fixture with an ENABLED event source mapping to a
   * real function that is busy with one message, so the queue has a reader and
   * still has its messages waiting (harbour.ts). It is booted new on every
   * call, and the reader is busy for HOLD_MS from then: drive inside that time.
   *
   * `own` is the harbour fixture again, booted anew on every call and shared
   * with nobody: for a board that spends the queues, enables a trigger, or holds
   * messages, and must not hand that state to the next board.
   */
  instance(kind: 'enforce' | 'empty' | 'fresh' | 'map' | 'mapLarge' | 'wire' | 'wireEnforce' | 'reader' | 'own'): Promise<Inst>;
  /** Log a thing the report should carry beside the row (a note, not a failure). */
  note(text: string): void;
}

export type Driver =
  | {
      /** Put the real page into the board's state. */
      drive(c: Ctx): Promise<void>;
      /** Why the page legitimately cannot be this board, if only part of it can. */
      note?: string;
    }
  | {
      /** There is nothing to drive, and this says why. Shown in the report as SKIP. */
      skip: string;
    };

export type DriverTable = Record<string, Driver>;
