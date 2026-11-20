import { api, call, messageOf } from '@/api/client'
import { ref, watch, type Ref } from 'vue'

/** Keep the signed URL tied to the selected version, including during a refresh. */
export function useSnapshotLinks(id: Readonly<Ref<string | undefined>>) {
  const viewURL = ref('')
  const linkError = ref('')
  const downloading = ref(false)
  const retry = ref(0)

  watch([id, retry], ([snapshotId], _, onCleanup) => {
    let active = true
    let timer: ReturnType<typeof setTimeout>
    onCleanup(() => { active = false; clearTimeout(timer) })
    viewURL.value = ''
    linkError.value = ''
    if (!snapshotId) return

    async function sign(selected: string) {
      try {
        const r = await call(api.POST('/api/v1/snapshots/{id}/view-url', { params: { path: { id: selected } } }))
        if (!active) return
        viewURL.value = r.url
        const delay = new Date(r.expires_at).getTime() - Date.now() - 120_000
        timer = setTimeout(() => void sign(selected), Math.max(60_000, delay))
      } catch (e) {
        if (active) { viewURL.value = ''; linkError.value = messageOf(e) }
      }
    }
    void sign(snapshotId)
  }, { immediate: true, flush: 'sync' })

  async function download() {
    const selected = id.value
    if (!selected || downloading.value) return
    downloading.value = true
    try {
      const r = await call(api.POST('/api/v1/snapshots/{id}/download-url', { params: { path: { id: selected } } }))
      if (selected === id.value) location.href = r.url
    } catch (e) {
      if (selected === id.value) linkError.value = messageOf(e)
    } finally {
      downloading.value = false
    }
  }

  return { viewURL, linkError, download, downloading, retryView: () => retry.value++ }
}
