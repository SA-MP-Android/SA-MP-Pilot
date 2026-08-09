import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from './locales/en'
import ru from './locales/ru'
import zh from './locales/zh'

export const supportedLanguages = ['en', 'zh', 'ru'] as const
export type SupportedLanguage = (typeof supportedLanguages)[number]

function readSavedLanguage() {
  try {
    return typeof localStorage?.getItem === 'function' ? localStorage.getItem('language') : null
  } catch {
    return null
  }
}

const savedLanguage = readSavedLanguage()
const browserLanguage = navigator.language.toLowerCase().split('-')[0]
const initialLanguage = supportedLanguages.includes(savedLanguage as SupportedLanguage)
  ? savedLanguage!
  : supportedLanguages.includes(browserLanguage as SupportedLanguage)
    ? browserLanguage
    : 'en'

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, zh: { translation: zh }, ru: { translation: ru } },
  lng: initialLanguage,
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
})

export function changeLanguage(language: SupportedLanguage) {
  try {
    if (typeof localStorage?.setItem === 'function') localStorage.setItem('language', language)
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
  return i18n.changeLanguage(language)
}

export default i18n
