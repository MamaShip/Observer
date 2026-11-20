// The version the server reports (GET /site): a release tag such as v0.3.0,
// git describe output for other builds (v0.3.0-2-g1ec2416, maybe -dirty),
// or "dev".

const repo = 'https://github.com/MamaShip/Observer'

/** sourceURL links a version to its code on GitHub, or returns null. */
export function sourceURL(version: string | undefined): string | null {
  if (!version) return null
  if (/^v\d+\.\d+\.\d+(-(alpha|beta|rc)[0-9.]*)?$/.test(version)) return `${repo}/tree/${version}`
  const m = /-g([0-9a-f]{7,40})$/.exec(version) // a build between releases, without local changes
  return m ? `${repo}/commit/${m[1]}` : null
}
