const ua = typeof navigator === 'undefined' ? '' : navigator.userAgent

/** WeChat's in-app browser cannot download files or print. */
export const inWeChat = /MicroMessenger/i.test(ua)
export const isIOS = /iPhone|iPad|iPod/i.test(ua) || (/Macintosh/.test(ua) && typeof document !== 'undefined' && 'ontouchend' in document)
export const isAndroid = /Android/i.test(ua)
export const isMobile = isIOS || isAndroid
