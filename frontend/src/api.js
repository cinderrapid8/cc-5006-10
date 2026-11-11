async function request(path, options = {}) {
  const res = await fetch(path, {
    headers: options.body ? { 'Content-Type': 'application/json' } : undefined,
    ...options
  })
  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    throw new Error(data?.error || `HTTP ${res.status}`)
  }
  return data
}

export const api = {
  health: () => request('/api/health'),

  async uploadVideo(file, onProgress) {
    const form = new FormData()
    form.append('video', file)
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest()
      xhr.open('POST', '/api/videos')
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) resolve(JSON.parse(xhr.responseText))
        else reject(new Error(xhr.responseText || `HTTP ${xhr.status}`))
      }
      xhr.onerror = () => reject(new Error('upload failed'))
      if (onProgress) {
        xhr.upload.onprogress = (e) => onProgress(e.lengthComputable ? e.loaded / e.total : 0)
      }
      xhr.send(form)
    })
  },

  createProject: (payload) => request('/api/projects', { method: 'POST', body: JSON.stringify(payload) }),
  getProject: (id) => request(`/api/projects/${id}`),
  updateProject: (id, payload) => request(`/api/projects/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  rebuild: (id) => request(`/api/projects/${id}/rebuild`, { method: 'POST' }),

  startExport: (projectId, cutMode) =>
    request('/api/exports', { method: 'POST', body: JSON.stringify({ projectId, cutMode }) }),
  exportStatus: (jobId) => request(`/api/exports/${jobId}/status`),
  cancelExport: (jobId) => request(`/api/exports/${jobId}/cancel`, { method: 'POST' }),
  exportFileUrl: (jobId) => `/api/exports/${jobId}/file`,
  exportManifestUrl: (jobId) => `/api/exports/${jobId}/manifest`
}
