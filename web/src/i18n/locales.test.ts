import { describe, expect, it } from 'vitest'
import en from './locales/en'
import ru from './locales/ru'
import zh from './locales/zh'

function translationKeys(value: object, prefix = ''): string[] {
  return Object.entries(value).flatMap(([key, child]) => {
    const path = prefix ? `${prefix}.${key}` : key
    return typeof child === 'object' && child !== null ? translationKeys(child, path) : [path]
  })
}

describe('translation resources', () => {
  it('keeps English, Chinese, and Russian keys synchronized', () => {
    const englishKeys = translationKeys(en).sort()
    expect(translationKeys(zh).sort()).toEqual(englishKeys)
    expect(translationKeys(ru).sort()).toEqual(englishKeys)
  })

  it('provides non-empty values for every supported language', () => {
    for (const resource of [en, zh, ru]) {
      expect(translationKeys(resource)).not.toHaveLength(0)
      expect(JSON.stringify(resource)).not.toContain(':""')
    }
  })

  it('localizes the dialogs tab for every language', () => {
    expect(en.tabs.dialogs).toBe('Dialogs')
    expect(zh.tabs.dialogs).toBe('对话框')
    expect(ru.tabs.dialogs).toBe('Диалоги')
  })
})
