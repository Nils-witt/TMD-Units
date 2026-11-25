import type { UnitAlarm } from '../api/types';

type Listener = (alarm: UnitAlarm) => void;

const listeners = new Set<Listener>();

/** Tells every listener (the alarm snackbar) about an alarm pushed over the
 * unit event stream. */
export function notifyAlarm(alarm: UnitAlarm): void {
  for (const listener of listeners) listener(alarm);
}

/** Calls listener for every pushed alarm; returns the unsubscribe function. */
export function onAlarm(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
