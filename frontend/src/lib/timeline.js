// Pure timeline helpers — unit tested with Vitest, no Vue dependency.

// Reorder blocks: move the block at fromIndex to toIndex.
export function moveBlock(order, fromIndex, toIndex) {
  if (fromIndex === toIndex) return [...order]
  if (fromIndex < 0 || fromIndex >= order.length || toIndex < 0 || toIndex >= order.length) {
    throw new RangeError('index out of bounds')
  }
  const next = [...order]
  const [moved] = next.splice(fromIndex, 1)
  next.splice(toIndex, 0, moved)
  return next
}

// Toggle inclusion; returns a new map.
export function toggleIncluded(included, blockId) {
  return { ...included, [blockId]: !included[blockId] }
}

// A block is exportable only when it has renderable pieces.
// Empty (fully gapped) blocks must never silently enter the export.
export function isExportable(block) {
  return Array.isArray(block.pieces) && block.pieces.length > 0 && !block.empty
}

// Human-readable coverage summary for a block, surfacing shortages explicitly.
export function coverageSummary(block) {
  const flags = []
  if (block.shortStart) flags.push('开头被录像边界截断')
  if (block.shortEnd) flags.push('结尾被录像边界截断')
  if (block.crossedGap) flags.push(`跨越缺口，缺失 ${block.missingInGap ?? ''} 秒`)
  if (block.empty) flags.push('整段落在缺口内，无可渲染画面')
  return flags.length ? flags.join('；') : '完整覆盖'
}

// Position a block on a 0..100 timeline lane given video duration.
export function blockStyle(block, durationSec) {
  if (!durationSec) return { left: '0%', width: '0%' }
  const left = Math.max(0, (block.start / durationSec) * 100)
  const width = Math.min(100 - left, ((block.end - block.start) / durationSec) * 100)
  return { left: `${left}%`, width: `${Math.max(width, 0.5)}%` }
}

// Merge event refs into a display label, preserving every occurrence
// (no de-duplication by event number).
export function blockLabel(block) {
  return (block.refs || [])
    .map((r) => `${r.eventId}${r.occurrence ? `#${r.occurrence}` : ''}(${r.type})`)
    .join(' + ')
}

// Parse mm:ss / seconds input used by anchor forms.
export function parseTimeInput(value) {
  const s = String(value).trim()
  if (!s) return NaN
  if (!s.includes(':')) return Number(s)
  const parts = s.split(':').map(Number)
  if (parts.some(Number.isNaN)) return NaN
  return parts.reduce((acc, p) => acc * 60 + p, 0)
}
