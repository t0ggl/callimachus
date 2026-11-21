import type { MetadataCandidate } from '../types';

export const ALL_LANGUAGES = 'all';

const LANGUAGE_ALIASES: Record<string, string> = {
  en: 'en', eng: 'en', english: 'en',
  es: 'es', spa: 'es', spanish: 'es', 'español': 'es', espanol: 'es', castilian: 'es',
  fr: 'fr', fra: 'fr', fre: 'fr', french: 'fr', 'français': 'fr', francais: 'fr',
  de: 'de', deu: 'de', ger: 'de', german: 'de', deutsch: 'de',
  pt: 'pt', por: 'pt', portuguese: 'pt', 'português': 'pt', portugues: 'pt',
  it: 'it', ita: 'it', italian: 'it', italiano: 'it',
  nl: 'nl', nld: 'nl', dut: 'nl', dutch: 'nl', nederlands: 'nl', flemish: 'nl',
  ru: 'ru', rus: 'ru', russian: 'ru', 'русский': 'ru',
  ja: 'ja', jpn: 'ja', japanese: 'ja', '日本語': 'ja',
  zh: 'zh', zho: 'zh', chi: 'zh', chinese: 'zh', mandarin: 'zh', cantonese: 'zh', '中文': 'zh',
  ko: 'ko', kor: 'ko', korean: 'ko', '한국어': 'ko',
  pl: 'pl', pol: 'pl', polish: 'pl', polski: 'pl',
  sv: 'sv', swe: 'sv', swedish: 'sv', svenska: 'sv',
  no: 'no', nor: 'no', nb: 'no', nob: 'no', nn: 'no', nno: 'no',
  norwegian: 'no', norsk: 'no', bokmal: 'no', 'bokmål': 'no', nynorsk: 'no',
  da: 'da', dan: 'da', danish: 'da', dansk: 'da',
  fi: 'fi', fin: 'fi', finnish: 'fi', suomi: 'fi',
  ar: 'ar', ara: 'ar', arabic: 'ar', 'العربية': 'ar',
  hi: 'hi', hin: 'hi', hindi: 'hi', 'हिन्दी': 'hi',
  tr: 'tr', tur: 'tr', turkish: 'tr', 'türkçe': 'tr', turkce: 'tr',
  cs: 'cs', ces: 'cs', cze: 'cs', czech: 'cs', 'čeština': 'cs', cestina: 'cs',
  hu: 'hu', hun: 'hu', hungarian: 'hu', magyar: 'hu',
  ro: 'ro', ron: 'ro', rum: 'ro', romanian: 'ro', 'română': 'ro', romana: 'ro',
  el: 'el', ell: 'el', gre: 'el', greek: 'el', 'ελληνικά': 'el',
  he: 'he', heb: 'he', iw: 'he', hebrew: 'he', 'עברית': 'he',
  uk: 'uk', ukr: 'uk', ukrainian: 'uk', 'українська': 'uk',
  id: 'id', ind: 'id', in: 'id', indonesian: 'id', 'bahasa indonesia': 'id',
  vi: 'vi', vie: 'vi', vietnamese: 'vi', 'tiếng việt': 'vi',
  th: 'th', tha: 'th', thai: 'th', 'ไทย': 'th',
  ca: 'ca', cat: 'ca', catalan: 'ca', 'català': 'ca',
  bg: 'bg', bul: 'bg', bulgarian: 'bg',
  hr: 'hr', hrv: 'hr', croatian: 'hr', hrvatski: 'hr',
  sk: 'sk', slk: 'sk', slo: 'sk', slovak: 'sk',
  sl: 'sl', slv: 'sl', slovenian: 'sl', slovene: 'sl',
  sr: 'sr', srp: 'sr', serbian: 'sr',
  fa: 'fa', fas: 'fa', per: 'fa', persian: 'fa', farsi: 'fa',
  la: 'la', lat: 'la', latin: 'la',
  ga: 'ga', gle: 'ga', irish: 'ga',
  cy: 'cy', cym: 'cy', wel: 'cy', welsh: 'cy',
  is: 'is', isl: 'is', ice: 'is', icelandic: 'is',
  et: 'et', est: 'et', estonian: 'et',
  lv: 'lv', lav: 'lv', latvian: 'lv',
  lt: 'lt', lit: 'lt', lithuanian: 'lt',
  ms: 'ms', msa: 'ms', may: 'ms', malay: 'ms',
  tl: 'tl', tgl: 'tl', fil: 'tl', tagalog: 'tl', filipino: 'tl',
};

const UNKNOWN_LANGUAGE_TOKENS = new Set([
  'und', 'un', 'unknown', 'unspecified', 'none', 'n/a', 'zxx', 'qaa', 'mul',
]);

const LANGUAGE_NAMES: Record<string, string> = {
  en: 'English',
  es: 'Spanish',
  fr: 'French',
  de: 'German',
  pt: 'Portuguese',
  it: 'Italian',
  nl: 'Dutch',
  ru: 'Russian',
  ja: 'Japanese',
  zh: 'Chinese',
  ko: 'Korean',
  pl: 'Polish',
  sv: 'Swedish',
  no: 'Norwegian',
  da: 'Danish',
  fi: 'Finnish',
  ar: 'Arabic',
  hi: 'Hindi',
  tr: 'Turkish',
  cs: 'Czech',
  hu: 'Hungarian',
  ro: 'Romanian',
  el: 'Greek',
  he: 'Hebrew',
  uk: 'Ukrainian',
  id: 'Indonesian',
  vi: 'Vietnamese',
  th: 'Thai',
  ca: 'Catalan',
  bg: 'Bulgarian',
  hr: 'Croatian',
  sk: 'Slovak',
  sl: 'Slovenian',
  sr: 'Serbian',
  fa: 'Persian',
  la: 'Latin',
  ga: 'Irish',
  cy: 'Welsh',
  is: 'Icelandic',
  et: 'Estonian',
  lv: 'Latvian',
  lt: 'Lithuanian',
  ms: 'Malay',
  tl: 'Tagalog',
};

export const COMMON_LANGUAGES = [
  'en', 'es', 'fr', 'de', 'pt', 'it', 'nl', 'ru', 'ja', 'zh', 'ko', 'pl', 'sv',
] as const;

let displayNames: Intl.DisplayNames | null | undefined;
function getIntlLanguageName(code: string): string | undefined {
  if (displayNames === undefined) {
    try {
      displayNames = typeof Intl !== 'undefined' && 'DisplayNames' in Intl
        ? new Intl.DisplayNames(['en'], { type: 'language' })
        : null;
    } catch {
      displayNames = null;
    }
  }
  if (!displayNames) return undefined;
  try {
    const resolved = displayNames.of(code);
    if (resolved && resolved.toLowerCase() !== code.toLowerCase()) {
      return resolved;
    }
  } catch {
    // Ignore unsupported subtags.
  }
  return undefined;
}

export function normalizeLanguageCode(raw?: string | null): string {
  if (!raw) return '';
  let value = raw.trim().toLowerCase();
  if (value.startsWith('/languages/')) {
    value = value.slice('/languages/'.length).trim();
  }
  if (!value || UNKNOWN_LANGUAGE_TOKENS.has(value)) {
    return '';
  }
  if (LANGUAGE_ALIASES[value]) {
    return LANGUAGE_ALIASES[value];
  }
  const parts = value.split(/[-_]/);
  if (parts.length === 0 || parts.length > 3) {
    return '';
  }
  const [primary, ...rest] = parts;
  if (!primary || UNKNOWN_LANGUAGE_TOKENS.has(primary)) {
    return '';
  }
  for (const sub of rest) {
    if (!/^(?:[a-z]{2}|[a-z]{4}|[0-9]{3})$/.test(sub)) {
      return '';
    }
  }
  if (LANGUAGE_ALIASES[primary]) {
    return LANGUAGE_ALIASES[primary];
  }
  if (/^[a-z]{2,3}$/.test(primary)) {
    return primary;
  }
  return '';
}

export function normalizeLanguageFilter(raw?: string | null): string {
  if (!raw) return '';
  const value = raw.trim().toLowerCase();
  if (value === ALL_LANGUAGES || value === '*') {
    return ALL_LANGUAGES;
  }
  return normalizeLanguageCode(value);
}

export function formatLanguageName(code: string): string {
  if (code === ALL_LANGUAGES) return 'All languages';
  const normalized = normalizeLanguageCode(code) || code;
  return LANGUAGE_NAMES[normalized] ?? getIntlLanguageName(normalized) ?? normalized.toUpperCase();
}

export function formatLanguageLabel(code: string): string {
  if (code === ALL_LANGUAGES) return 'All languages';
  const normalized = normalizeLanguageCode(code) || code;
  const name = LANGUAGE_NAMES[normalized] ?? getIntlLanguageName(normalized);
  return name ? `${name} (${normalized})` : normalized.toUpperCase();
}

export interface LanguageOption {
  value: string;
  label: string;
}

export function buildLanguageOptions(
  defaultLanguage: string,
  availableLanguages: string[],
  selectedLanguage: string,
): LanguageOption[] {
  const seen = new Set<string>();
  const options: LanguageOption[] = [];

  const add = (raw: string) => {
    const code = normalizeLanguageFilter(raw);
    if (!code || seen.has(code)) return;
    seen.add(code);
    options.push({ value: code, label: formatLanguageLabel(code) });
  };

  add(ALL_LANGUAGES);
  if (defaultLanguage && defaultLanguage !== ALL_LANGUAGES) {
    add(defaultLanguage);
  }
  if (selectedLanguage && selectedLanguage !== ALL_LANGUAGES) {
    add(selectedLanguage);
  }
  for (const lang of availableLanguages) {
    add(lang);
  }
  for (const lang of COMMON_LANGUAGES) {
    add(lang);
  }
  return options;
}

export function filterCandidatesByLanguage(
  candidates: MetadataCandidate[],
  selectedLanguage: string,
): MetadataCandidate[] {
  const filter = normalizeLanguageFilter(selectedLanguage);
  if (!filter || filter === ALL_LANGUAGES) {
    return candidates.map((candidate) => {
      const normalized = normalizeLanguageCode(candidate.language);
      return normalized && normalized !== candidate.language
        ? { ...candidate, language: normalized }
        : candidate;
    });
  }
  const filtered: MetadataCandidate[] = [];
  for (const candidate of candidates) {
    const normalized = normalizeLanguageCode(candidate.language);
    if (!normalized) {
      filtered.push(candidate.language ? { ...candidate, language: undefined } : candidate);
    } else if (normalized === filter) {
      filtered.push(normalized !== candidate.language ? { ...candidate, language: normalized } : candidate);
    }
  }
  return filtered;
}
