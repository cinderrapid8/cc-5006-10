<template>
  <main>
    <header>
      <h1>体育赛事精华组装服务</h1>
      <span v-if="project" class="muted">工程 {{ project.id }} · v{{ project.version }}</span>
    </header>

    <ImportPanel @video="onVideo" @build="buildProject" />

    <section v-if="project" class="panel">
      <h2>每类事件前后保留时长（秒）</h2>
      <div v-for="type in eventTypes" :key="type" class="row padding-row">
        <strong>{{ type }}</strong>
        <label>前 <input v-model.number="paddings[type].beforeSec" type="number" min="0" step="0.5" /></label>
        <label>后 <input v-model.number="paddings[type].afterSec" type="number" min="0" step="0.5" /></label>
      </div>
      <div class="row">
        <button @click="applyPaddings">应用并重算片段</button>
        <button @click="saveProject">保存选片工程</button>
        <button @click="reloadProject">重新加载已保存工程</button>
        <label>
          重叠政策：
          <select v-model="project.overlapPolicy" @change="rebuild">
            <option value="merge">合并</option>
            <option value="separate">独立保留</option>
          </select>
        </label>
      </div>
      <p v-if="notice" class="ok">{{ notice }}</p>
      <p v-if="error" class="error">{{ error }}</p>
    </section>

    <Timeline
      v-if="project"
      :blocks="project.blocks"
      :order="project.blockOrder"
      :unmapped="project.unmapped"
      :duration="project.videoMeta.durationSec"
      @move="onMove"
      @toggle="onToggle"
    />

    <ExportPanel
      v-if="project"
      :project-id="project.id"
      :blocks="project.blocks"
      :order="project.blockOrder"
    />
  </main>
</template>

<script setup>
import { computed, ref } from 'vue'
import ImportPanel from './components/ImportPanel.vue'
import Timeline from './components/Timeline.vue'
import ExportPanel from './components/ExportPanel.vue'
import { api } from './api.js'
import { moveBlock, toggleIncluded } from './lib/timeline.js'

const project = ref(null)
const paddings = ref({})
const notice = ref('')
const error = ref('')
let videoInfo = null

const eventTypes = computed(() => [...new Set((project.value?.events || []).map((e) => e.type))])

function onVideo(info) {
  videoInfo = info
}

async function buildProject(payload) {
  error.value = ''
  try {
    project.value = await api.createProject(payload)
    seedPaddings()
  } catch (err) {
    error.value = err.message
  }
}

function seedPaddings() {
  const next = {}
  for (const t of eventTypes.value) {
    next[t] = project.value.paddings?.[t] || { beforeSec: 5, afterSec: 5 }
  }
  paddings.value = next
}

async function applyPaddings() {
  await api.updateProject(project.value.id, { paddings: paddings.value })
  await rebuild()
}

async function rebuild() {
  project.value = await api.rebuild(project.value.id)
  seedPaddings()
  notice.value = '候选片段已按新参数重算'
}

async function saveProject() {
  project.value = await api.updateProject(project.value.id, {
    blockOrder: project.value.blockOrder,
    paddings: paddings.value
  })
  notice.value = `工程已保存（版本 ${project.value.version}）`
}

async function reloadProject() {
  project.value = await api.getProject(project.value.id)
  notice.value = '已从磁盘重新加载工程'
}

async function onMove({ from, to }) {
  project.value.blockOrder = moveBlock(project.value.blockOrder, from, to)
  await api.updateProject(project.value.id, { blockOrder: project.value.blockOrder })
}

async function onToggle(blockId) {
  const included = {}
  for (const b of project.value.blocks) included[b.id] = b.included
  const next = toggleIncluded(included, blockId)
  project.value = await api.updateProject(project.value.id, { included: next })
}
</script>
