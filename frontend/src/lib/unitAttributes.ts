import type { UnitAttribute, UnitAttributeType } from '../api/types';

export const unitAttributeTypes = [
  'text',
  'number',
  'boolean',
  'date',
  'url',
] as const satisfies readonly UnitAttributeType[];

/** These match the server's limits. */
export const MAX_ATTRIBUTES = 50;
export const MAX_ATTRIBUTE_KEY_LENGTH = 64;
export const MAX_ATTRIBUTE_TEXT_LENGTH = 1024;
export const MAX_ATTRIBUTE_URL_LENGTH = 2048;

/** An attribute as it is being edited. `text` holds the typed value of every
 * type but boolean, which uses `flag`, so switching the type keeps it. */
export interface AttributeDraft {
  /** Identifies the row for React; not sent. */
  id: number;
  key: string;
  type: UnitAttributeType;
  text: string;
  flag: boolean;
}

/** What is wrong with a draft, as translation keys under `unitAttributes.errors`. */
export interface AttributeDraftErrors {
  key?: 'keyRequired' | 'keyDuplicate' | 'keyInvalid';
  value?: 'text' | 'number' | 'date' | 'url';
}

let nextDraftId = 0;

export function newAttributeDraft(): AttributeDraft {
  return { id: nextDraftId++, key: '', type: 'text', text: '', flag: false };
}

export function toAttributeDrafts(attributes: UnitAttribute[]): AttributeDraft[] {
  return attributes.map((a) => ({
    ...newAttributeDraft(),
    key: a.key,
    type: a.type,
    text: a.type === 'boolean' ? '' : String(a.value),
    flag: a.type === 'boolean' && a.value,
  }));
}

/** Rows with neither a key nor a value are ignored rather than rejected. */
function isBlank(d: AttributeDraft): boolean {
  return d.key.trim() === '' && (d.type === 'boolean' || d.text.trim() === '');
}

/** Parses a decimal typed with either "." or "," as separator; NaN if it isn't one. */
function parseNumber(text: string): number {
  const v = text.trim().replace(',', '.');
  return v === '' ? NaN : Number(v);
}

// The server rejects control characters in keys, and in text values all but
// newlines and tabs.
// eslint-disable-next-line no-control-regex
const KEY_CONTROL = /[\u0000-\u001f\u007f-\u009f]/;
// eslint-disable-next-line no-control-regex
const TEXT_CONTROL = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/;

function validDate(text: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text.trim());
  if (!m) return false;
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const date = new Date(Date.UTC(y, mo - 1, d));
  return date.getUTCFullYear() === y && date.getUTCMonth() === mo - 1 && date.getUTCDate() === d;
}

function validUrl(text: string): boolean {
  const v = text.trim();
  if (v.length > MAX_ATTRIBUTE_URL_LENGTH) return false;
  try {
    const url = new URL(v);
    return (url.protocol === 'http:' || url.protocol === 'https:') && url.host !== '';
  } catch {
    return false;
  }
}

/** Validates drafts like the server does; one entry per draft, empty when it is fine. */
export function attributeDraftErrors(drafts: AttributeDraft[]): AttributeDraftErrors[] {
  const counts = new Map<string, number>();
  for (const d of drafts) {
    const k = d.key.trim().toLowerCase();
    if (k) counts.set(k, (counts.get(k) ?? 0) + 1);
  }
  return drafts.map((d): AttributeDraftErrors => {
    if (isBlank(d)) return {};
    const errors: AttributeDraftErrors = {};
    const key = d.key.trim();
    if (key === '') errors.key = 'keyRequired';
    else if ([...key].length > MAX_ATTRIBUTE_KEY_LENGTH || KEY_CONTROL.test(key))
      errors.key = 'keyInvalid';
    else if ((counts.get(key.toLowerCase()) ?? 0) > 1) errors.key = 'keyDuplicate';
    switch (d.type) {
      case 'text':
        if ([...d.text].length > MAX_ATTRIBUTE_TEXT_LENGTH || TEXT_CONTROL.test(d.text))
          errors.value = 'text';
        break;
      case 'number':
        if (!Number.isFinite(parseNumber(d.text))) errors.value = 'number';
        break;
      case 'date':
        if (!validDate(d.text)) errors.value = 'date';
        break;
      case 'url':
        if (!validUrl(d.text)) errors.value = 'url';
        break;
    }
    return errors;
  });
}

/** Converts valid drafts to the attributes to send, leaving out blank rows. */
export function attributesFromDrafts(drafts: AttributeDraft[]): UnitAttribute[] {
  return drafts
    .filter((d) => !isBlank(d))
    .map((d): UnitAttribute => {
      const key = d.key.trim();
      switch (d.type) {
        case 'text':
          return { key, type: 'text', value: d.text };
        case 'number':
          return { key, type: 'number', value: parseNumber(d.text) };
        case 'boolean':
          return { key, type: 'boolean', value: d.flag };
        case 'date':
          return { key, type: 'date', value: d.text.trim() };
        case 'url':
          return { key, type: 'url', value: d.text.trim() };
      }
    });
}
