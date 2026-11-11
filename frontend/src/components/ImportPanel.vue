<template>
  <section class="panel">
    <h2>1. 导入录像与事件清单</h2>
    <div class="row">
      <input type="file" accept="video/*" @change="onVideo" />
      <span v-if="uploading" class="muted">上传中 {{ Math.floor(progress * 100) }}%</span>
    </div>
    <div v-if="video" class="video-meta">
      <video :src="videoUrl" controls preload="metadata"></video>
      <ul>
        <li>时长：{{ video.durationSec.toFixed(2) }} 秒</li>
        <li>规格：{{ video.width }}×{{ video.height }}，{{ video.codec }}</li>
        <li>音频：{{ video.hasAudio ? '有' : '无（导出将不含音轨）' }}</li>
      </ul>
    </div>

    <h3>事件清单（JSON）</h3>
    <p class="muted">
      字段 eventId / type / clock（支持 mm:ss、90+3:12、秒数），可附 team、detail。
      重复事件编号请保留为多条记录，系统按出现次序区分。
    </p>
    <textarea v-model="eventsText" rows="8" spellcheck="false"></textarea>

    <h3>锚点（记录时钟 → 录像时间，至少两个）</h3>
    <table v-if="anchors.length">
      <thead><tr><th>说明</th><th>记录时钟</th><th>录像时间(秒)</th><th></th></tr></thead>
      <tbody>
        <tr v-for="(a, i) in anchors" :key="i">
          <td><input v-model="a.label" /></td>
          <td><input v-model="a.clock" /></td>
          <td><input v-model="a.video" type="number" step="0.1" /></td>
          <td><button class="danger" @click="anchors.splice(i, 1)">删除</button></td>
        </tr>
      </tbody>
    </table>
    <button @click="anchors.push({ label: '', clock: '', video: '' })">添加锚点</button>

    <h3>缺口（可选，录像时间秒）</h3>
    <div v-for="(g, i) in gaps" :key="i" class="row">
      <input v-model.number="g.start" type="number" step="0.1" placeholder="起" />
      <input v-model.number="g.end" type="number" step="0.1" placeholder="止" />
      <button class="danger" @click="gaps.splice(i, 1)">删除缺口</button>
    </div>
    <button @click="gaps.push({ start: null, end: null })">添加缺口</button>

    <div class="row build-row">
      <label>
        重叠政策：
        <select v-model="overlapPolicy">
          <option value="merge">合并（保留全部事件引用）</option>
          <option value="separate">独立保留</option>
        </select>
      </label>
      <button class="primary" :disabled="!canBuild" @click="$emit('build', payload)">生成候选片段</button>
    </div>
    <p v-if="error" class="error">{{ error }}</p>
  </section>
</template>

<script setup>
import { computed, ref } from 'vue'
import { api } from '../api.js'

const emit = defineEmits(['build', 'video'])
const video = ref(null)
const videoUrl = ref('')
const uploading = ref(false)
const progress = ref(0)
const eventsText = ref(JSON.stringify([
  { eventId: 'E1', type: 'goal', clock: '0:10', detail: '示例：开局进球' },
  { eventId: 'E2', type: 'foul', clock: '0:22', detail: '示例：犯规' }
], null, 2))
const anchors = ref([
  { label: '开球', clock: '0:00', video: '0' },
  { label: '中场', clock: '45:00', video: '1800' }
])
const gaps = ref([])
const overlapPolicy = ref('merge')
const error = ref('')

const canBuild = computed(() => Boolean(video.value && eventsText.value.trim() && anchors.value.length >= 2))
const payload = computed(() => ({
  name: 'highlight-project',
  videoPath: video.value.path,
  anchors: anchors.value,
  events: JSON.parse(eventsText.value || '[]'),
  gaps: gaps.value.filter((g) => Number.isFinite(g.start) && Number.isFinite(g.end)),
  overlapPolicy: overlapPolicy.value,
  paddings: {}
}))

async function onVideo(e) {
  const file = e.target.files[0]
  if (!file) return
  uploading.value = true
  error.value = ''
  try {
    const info = await api.uploadVideo(file, (p) => { progress.value = p })
    video.value = info
    videoUrl.value = `/api/videos/${info.path.split('/').pop()}`
    emit('video', info)
  } catch (err) {
    error.value = err.message
  } finally {
    uploading.value = false
  }
}
</script>
