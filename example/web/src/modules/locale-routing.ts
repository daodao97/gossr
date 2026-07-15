import {
  availableLocales,
  defaultLocale,
  isSupportedLocale,
  type SupportedLocale,
} from './i18n'
import { parsePathTarget, type ParsedPathTarget } from './navigation'

export interface LocaleRouteState {
  locale: SupportedLocale
  explicit: boolean
}

export function parseLocaleRouteState(path: string): LocaleRouteState {
  const firstSegment = path.replace(/^\/+/, '').split('/')[0]
  if (isSupportedLocale(firstSegment))
    return { locale: firstSegment, explicit: true }

  return { locale: defaultLocale, explicit: false }
}

export function stripLocalePrefix(path: string): string {
  for (const locale of availableLocales) {
    if (path === `/${locale}`)
      return '/'
    const prefix = `/${locale}/`
    if (path.startsWith(prefix))
      return `/${path.slice(prefix.length)}`
  }
  return path || '/'
}

export function localizeMenuTarget(target: ParsedPathTarget, state: LocaleRouteState): string {
  if (!state.explicit && state.locale === defaultLocale)
    return `${target.pathname}${target.search}${target.hash}`

  return `${withLocalePrefix(state.locale, target.pathname)}${target.search}${target.hash}`
}

export function switchLocaleTarget(rawTarget: string, locale: SupportedLocale): string {
  const parsed = parsePathTarget(rawTarget)
  const normalizedPath = stripLocalePrefix(parsed.pathname)
  const localizedPath = locale === defaultLocale ? normalizedPath : withLocalePrefix(locale, normalizedPath)
  return `${localizedPath}${parsed.search}${parsed.hash}`
}

function withLocalePrefix(locale: SupportedLocale, normalizedPath: string): string {
  if (normalizedPath === '/')
    return `/${locale}`
  return `/${locale}${normalizedPath}`
}
