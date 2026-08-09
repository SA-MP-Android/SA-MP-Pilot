import { describe, expect, it } from 'vitest'
import { cn, parseSAMPColors, stripSAMPColors } from './utils'
describe('cn', () => {
  it('merges tailwind conflicts', () => expect(cn('px-2', 'px-4')).toBe('px-4'))
})
describe('stripSAMPColors', () => {
  it('removes color tags from dialog response text', () => {
    expect(stripSAMPColors('{FF0000}first{00FF00} column')).toBe('first column')
  })
})
describe('parseSAMPColors', () => {
  it('parses six and eight digit inline colors', () => {
    expect(parseSAMPColors('white {FF0000}red {00FF0080}green')).toEqual([
      { text: 'white ', color: undefined },
      { text: 'red ', color: '#FF0000' },
      { text: 'green', color: '#00FF0080' },
    ])
  })

  it('keeps malformed color markers as text', () => {
    expect(parseSAMPColors('{oops}text')).toEqual([{ text: '{oops}text' }])
  })
})
