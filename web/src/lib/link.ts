// Users paste whatever WeChat's "copy link" or a share gives them; the
// article link is somewhere in it. The server does the real validation.
const linkRe = /https?:\/\/mp\.weixin\.qq\.com\/s[^\s<>"'，。；、）)】]*/i

/** extractLink returns the first WeChat article link in text, or null. */
export function extractLink(text: string): string | null {
  const m = linkRe.exec(text)
  return m ? m[0].replace(/[.,;!?]+$/, '') : null
}
