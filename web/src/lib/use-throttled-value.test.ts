// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useThrottledValue } from './use-throttled-value'

describe('useThrottledValue', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('shows the initial value immediately', () => {
    const { result } = renderHook(() => useThrottledValue('a', 500))
    expect(result.current).toBe('a')
  })

  it('defers updates to at most once per interval', () => {
    const { result, rerender } = renderHook(({ value }) => useThrottledValue(value, 500), {
      initialProps: { value: 'a' },
    })
    expect(result.current).toBe('a')

    rerender({ value: 'b' })
    act(() => {
      vi.advanceTimersByTime(100)
    })
    expect(result.current).toBe('a')

    rerender({ value: 'c' })
    act(() => {
      vi.advanceTimersByTime(100)
    })
    expect(result.current).toBe('a')

    act(() => {
      vi.advanceTimersByTime(400)
    })
    expect(result.current).toBe('c')
  })

  it('flushes immediately when enough time has passed', () => {
    const { result, rerender } = renderHook(({ value }) => useThrottledValue(value, 500), {
      initialProps: { value: 'a' },
    })
    act(() => {
      vi.advanceTimersByTime(600)
    })
    rerender({ value: 'b' })
    expect(result.current).toBe('b')
  })
})
