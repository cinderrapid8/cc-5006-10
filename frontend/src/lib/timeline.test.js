import { describe, it, expect } from 'vitest'
import {
  moveBlock,
  toggleIncluded,
  isExportable,
  coverageSummary,
  blockStyle,
  blockLabel,
  parseTimeInput
} from './timeline.js'

describe('moveBlock', () => {
  it('moves an item to a new position', () => {
    expect(moveBlock(['a', 'b', 'c'], 0, 2)).toEqual(['b', 'c', 'a'])
    expect(moveBlock(['a', 'b', 'c'], 2, 0)).toEqual(['c', 'a', 'b'])
  })
  it('returns a copy even when position is unchanged', () => {
    const order = ['a', 'b']
    const out = moveBlock(order, 1, 1)
    expect(out).toEqual(order)
    expect(out).not.toBe(order)
  })
  it('rejects out-of-range indices', () => {
    expect(() => moveBlock(['a'], 0, 5)).toThrow(RangeError)
  })
})

describe('toggleIncluded', () => {
  it('flips one block without mutating the map', () => {
    const map = { B0: true, B1: false }
    const out = toggleIncluded(map, 'B1')
    expect(out.B1).toBe(true)
    expect(map.B1).toBe(false)
  })
})

describe('isExportable', () => {
  it('requires renderable pieces', () => {
    expect(isExportable({ pieces: [{ start: 1, end: 2 }] })).toBe(true)
    expect(isExportable({ pieces: [], empty: true })).toBe(false)
    expect(isExportable({ empty: true, pieces: [{ start: 1, end: 2 }] })).toBe(false)
  })
})

describe('coverageSummary', () => {
  it('reports complete coverage', () => {
    expect(coverageSummary({})).toBe('完整覆盖')
  })
  it('surfaces short ends and gaps explicitly', () => {
    const s = coverageSummary({ shortEnd: true, crossedGap: true, missingInGap: 3 })
    expect(s).toContain('结尾被录像边界截断')
    expect(s).toContain('缺失 3 秒')
    expect(coverageSummary({ empty: true })).toContain('无可渲染画面')
  })
})

describe('blockStyle', () => {
  it('scales block bounds to percentages of video duration', () => {
    const style = blockStyle({ start: 10, end: 20 }, 60)
    expect(parseFloat(style.left)).toBeCloseTo(16.667, 2)
    expect(parseFloat(style.width)).toBeCloseTo(16.667, 2)
  })
  it('never returns a negative width', () => {
    const style = blockStyle({ start: 59, end: 60 }, 60)
    expect(parseFloat(style.width)).toBeGreaterThan(0)
  })
})

describe('blockLabel keeps every occurrence', () => {
  it('does not de-duplicate repeated event numbers', () => {
    const label = blockLabel({
      refs: [
        { eventId: 'E9', occurrence: 0, type: 'foul' },
        { eventId: 'E9', occurrence: 1, type: 'foul' }
      ]
    })
    expect(label).toBe('E9(foul) + E9#1(foul)')
  })
})

describe('parseTimeInput', () => {
  it('handles seconds and mm:ss', () => {
    expect(parseTimeInput('45')).toBe(45)
    expect(parseTimeInput('1:30')).toBe(90)
    expect(parseTimeInput('0:05')).toBe(5)
    expect(Number.isNaN(parseTimeInput('bad'))).toBe(true)
  })
})
