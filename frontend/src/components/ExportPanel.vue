<template>
  <section class="panel">
    <h2>3. 渲染精华</h2>
    <div class="row">
      <label>
        切点方式：
        <select v-model="cutMode">
          <option value="reencode">精确重编码（libx264，帧级准确）</option>
          <option value="keyframe">关键帧近似（流复制，切点吸附关键帧）</option>
        </select>
      </label>
      <button class="primary" :disabled="!canExport || running" @click="start">开始导出</button>
      <button v-if="running" class="danger" @click="cancel">取消导出</button>
    </div>
    <p class="muted">
      关键帧近似会把切点吸附到最近关键帧，成片清单会逐段记录请求区间与实际区间。
    </p>

    <div v-if="status" class="progress">
      <div class="bar" :style="{ width: `${Math.round((status.ratio || 0) * 100)}%` }"></div>
      <span>{{ status.message || '等待中' }}（{{ Math.round((status.ratio || 0) * 100) }}%）</span>
    </div>
    <p v-if="status?.error" class="error">导出状态：{{ status.error }}</p>

    <div v-if="manifest" class="manifest">
      <h3>成片清单（注明采用方式）</h3>
      <p><strong>切点方式：</strong>{{ manifest.cutMode }} — {{ manifest.cutNote }}</p>
      <table>
        <thead>
          <tr><th>块</th><th>来源事件</th><th>请求区间</th><th>实际区间</th><th>标记</th></tr>
        </thead>
        <tbody>
          <tr v-for="b in manifest.blocks" :key="b.blockId">
            <td>{{ b.blockId }}</td>
            <td>{{ b.segmentKeys.join(', ') }}</td>
            <td>
              <span v-for="(p, i) in b.planned" :key="i" class="piece">
                {{ p.start.toFixed(2) }}–{{ p.end.toFixed(2) }}
              </span>
            </td>
            <td>
              <span v-for="(p, i) in b.actual" :key="i" class="piece">
                {{ p.start.toFixed(2) }}–{{ p.end.toFixed(2) }}
              </span>
              <em v-if="b.note" class="warn">{{ b.note }}</em>
            </td>
            <td>
              <span v-if="b.shortStart || b.shortEnd" class="warn">边界截断</span>
              <span v-if="b.crossedGap" class="warn">跨缺口</span>
            </td>
          </tr>
        </tbody>
      </table>
      <div class="row">
        <a :href="downloadUrl" class="button">下载成片 MP4</a>
        <a :href="manifestUrl" class="button">下载清单 JSON</a>
      </div>
    </div>
  </section>
</template>

<script setup>
import { computed, onUnmounted, ref } from 'vue'
import { api } from '../api.js'

const props = defineProps({
  projectId: { type: String, required: true },
  blocks: { type: Array, required: true },
  order: { type: Array, required: true }
})

const cutMode = ref('reencode')
const running = ref(false)
const status = ref(null)
const manifest = ref(null)
const jobId = ref('')
let timer = null

const canExport = computed(() =>
  props.blocks.some((b) => b.included && b.pieces?.length)
)
const downloadUrl = computed(() => (jobId.value ? api.exportFileUrl(jobId.value) : '#'))
const manifestUrl = computed(() => (jobId.value ? api.exportManifestUrl(jobId.value) : '#'))

async function start() {
  manifest.value = null
  running.value = true
  status.value = { ratio: 0, message: '启动中' }
  const { jobId: id } = await api.startExport(props.projectId, cutMode.value)
  jobId.value = id
  timer = setInterval(poll, 500)
}

async function poll() {
  status.value = await api.exportStatus(jobId.value)
  if (status.value.done) {
    clearInterval(timer)
    running.value = false
    if (!status.value.error) {
      manifest.value = await (await fetch(api.exportManifestUrl(jobId.value))).json()
    }
  }
}

async function cancel() {
  await api.cancelExport(jobId.value)
}

onUnmounted(() => timer && clearInterval(timer))
</script>
