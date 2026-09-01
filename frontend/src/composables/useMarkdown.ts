import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { ref } from 'vue'

export function useMarkdown() {
  const isLoading = ref(false)

  async function renderMarkdown(markdown: string): Promise<string> {
    isLoading.value = true
    try {
      const html = await marked(markdown, {
        breaks: true,
        gfm: true
      })
      return DOMPurify.sanitize(html, {
        USE_PROFILES: { html: true },
        FORBID_TAGS: ['style', 'script'],
        FORBID_ATTR: ['onerror', 'onload', 'onclick']
      })
    } finally {
      isLoading.value = false
    }
  }

  return {
    isLoading,
    renderMarkdown
  }
}