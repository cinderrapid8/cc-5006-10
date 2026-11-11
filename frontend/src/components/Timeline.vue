<template>
  <section class="panel">
    <h2>2. 时间线：调整排序与删选</h2>
    <p class="muted">
      合并块会列出全部来源事件；短缺或跨缺口的块有明确标记，整段落入缺口的块默认排除且不能导出。
    </p>

    <div class="lane">
      <div
        v-for="(block, idx) in ordered"
        :key="block.id"
        class="clip"
        :class="clipClass(block)"
        :style="blockStyle(block, duration)"
        :title="coverageSummary(block)"
      >
        <span class="clip-id">{{ block.id }}</span>
      </div>
    </div>

    <table class="blocks">
      <thead>
        <tr>
          <th>顺序</th><th>块</th><th>事件引用（全部可追溯）</th><th>实际覆盖</th>
          <th>时长</th><th>状态</th><th>纳入</th><th>移动</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(block, idx) in ordered" :key="block.id" :class="{ excluded: !block.included }">
          <td>{{ idx + 1 }}</td>
          <td>{{ block.id }}</td>
          <td>{{ blockLabel(block) }}</td>
          <td>
            <span v-for="(p, i) in block.pieces" :key="i" class="piece">
              {{ p.start.toFixed(1) }}–{{ p.end.toFixed(1) }}
            </span>
            <em v-if="!block.pieces.length" class="warn">无可用画面</em>
          </td>
          <td>{{ block.duration.toFixed(1) }}s</td>
          <td>
            <span v-if="coverageSummary(block) !== '完整覆盖'" class="warn">{{ coverageSummary(block) }}</span>
            <span v-else class="ok">完整</span>
          </td>
          <td>
            <input
              type="checkbox"
              :checked="block.included"
              :disabled="!isExportable(block)"
              @change="$emit('toggle', block.id)"
            />
          </td>
          <td>
            <button :disabled="idx === 0" @click="$emit('move', { from: idx, to: idx - 1 })">↑</button>
            <button :disabled="idx === ordered.length - 1" @click="$emit('move', { from: idx, to: idx + 1 })">↓</button>
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="unmapped.length" class="pending">
      <h3>待处理事件（缺少锚点映射，未参与截取）</h3>
      <ul>
        <li v-for="(u, i) in unmapped" :key="i">
          {{ u.event.eventId }} · {{ u.event.type }} · 记录时钟 {{ u.event.clockText || u.event.clockSec }}
          — {{ u.reason }}
        </li>
      </ul>
      <p class="muted">这些事件不能因为数字相同就截取，请补充锚点后重新生成。</p>
    </div>
  </section>
</template>

<script setup>
import { computed } from 'vue'
import { blockStyle, blockLabel, coverageSummary, isExportable } from '../lib/timeline.js'

const props = defineProps({
  blocks: { type: Array, required: true },
  order: { type: Array, required: true },
  unmapped: { type: Array, default: () => [] },
  duration: { type: Number, required: true }
})
defineEmits(['move', 'toggle'])

// Keep block objects in the user-adjusted order; unknown ids append naturally.
const ordered = computed(() => {
  const byId = new Map(props.blocks.map((b) => [b.id, b]))
  const out = []
  const seen = new Set()
  for (const id of props.order) {
    const b = byId.get(id)
    if (b) { out.push(b); seen.add(id) }
  }
  for (const b of props.blocks) {
    if (!seen.has(b.id)) out.push(b)
  }
  return out
})

function clipClass(block) {
  return {
    short: block.shortStart || block.shortEnd,
    gap: block.crossedGap,
    empty: block.empty,
    off: !block.included
  }
}
</script>
