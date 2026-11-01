// A small QR code encoder: byte mode, error correction level M, versions
// 1-10 (up to 213 bytes), which is plenty for an otpauth:// address. Written to
// the QR Code Model 2 specification so no dependency is needed.

const ECC_PER_BLOCK_M = [0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26]
const BLOCKS_M = [0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5]
const MAX_VERSION = 10

function rawDataModules(ver: number): number {
  let result = (16 * ver + 128) * ver + 64
  if (ver >= 2) {
    const align = Math.floor(ver / 7) + 2
    result -= (25 * align - 10) * align - 55
    if (ver >= 7) result -= 36
  }
  return result
}

function dataCodewords(ver: number): number {
  return Math.floor(rawDataModules(ver) / 8) - ECC_PER_BLOCK_M[ver] * BLOCKS_M[ver]
}

function mulGF(x: number, y: number): number {
  let z = 0
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d)
    z ^= ((y >>> i) & 1) * x
  }
  return z
}

function rsDivisor(degree: number): number[] {
  const result: number[] = new Array(degree).fill(0)
  result[degree - 1] = 1
  let root = 1
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < degree; j++) {
      result[j] = mulGF(result[j], root)
      if (j + 1 < degree) result[j] ^= result[j + 1]
    }
    root = mulGF(root, 0x02)
  }
  return result
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const result: number[] = new Array(divisor.length).fill(0)
  for (const b of data) {
    const factor = b ^ (result.shift() as number)
    result.push(0)
    divisor.forEach((coef, i) => { result[i] ^= mulGF(coef, factor) })
  }
  return result
}

function alignPositions(ver: number): number[] {
  if (ver === 1) return []
  const n = Math.floor(ver / 7) + 2
  const size = ver * 4 + 17
  const step = Math.ceil((ver * 4 + 4) / (n * 2 - 2)) * 2
  const out = [6]
  for (let pos = size - 7; out.length < n; pos -= step) out.splice(1, 0, pos)
  return out
}

function utf8(text: string): number[] {
  return Array.from(new TextEncoder().encode(text))
}

/** The modules of a QR code for `text`: true is dark. Square, quiet zone not included. */
export function qrModules(text: string): boolean[][] {
  const bytes = utf8(text)
  let ver = 1
  for (; ver <= MAX_VERSION; ver++) {
    if (4 + (ver <= 9 ? 8 : 16) + 8 * bytes.length <= dataCodewords(ver) * 8) break
  }
  if (ver > MAX_VERSION) throw new RangeError('The text is too long for a QR code here')

  // Data bits: mode (byte), length, bytes, terminator, padding.
  const bits: number[] = []
  const put = (value: number, len: number) => { for (let i = len - 1; i >= 0; i--) bits.push((value >>> i) & 1) }
  put(0b0100, 4)
  put(bytes.length, ver <= 9 ? 8 : 16)
  for (const b of bytes) put(b, 8)
  const capacityBits = dataCodewords(ver) * 8
  put(0, Math.min(4, capacityBits - bits.length))
  put(0, (8 - (bits.length % 8)) % 8)
  for (let pad = 0xec; bits.length < capacityBits; pad ^= 0xec ^ 0x11) put(pad, 8)
  const data: number[] = []
  for (let i = 0; i < bits.length; i += 8) {
    let v = 0
    for (let j = 0; j < 8; j++) v = (v << 1) | bits[i + j]
    data.push(v)
  }

  // Error correction, interleaved across blocks.
  const numBlocks = BLOCKS_M[ver]
  const eccLen = ECC_PER_BLOCK_M[ver]
  const rawCodewords = Math.floor(rawDataModules(ver) / 8)
  const numShort = numBlocks - (rawCodewords % numBlocks)
  const shortLen = Math.floor(rawCodewords / numBlocks)
  const divisor = rsDivisor(eccLen)
  const blocks: number[][] = []
  for (let i = 0, k = 0; i < numBlocks; i++) {
    const dat = data.slice(k, k + shortLen - eccLen + (i < numShort ? 0 : 1))
    k += dat.length
    const ecc = rsRemainder(dat, divisor)
    if (i < numShort) dat.push(0)
    blocks.push(dat.concat(ecc))
  }
  const codewords: number[] = []
  for (let i = 0; i < blocks[0].length; i++) {
    blocks.forEach((block, j) => { if (i !== shortLen - eccLen || j >= numShort) codewords.push(block[i]) })
  }

  // The grid, with the function patterns.
  const size = ver * 4 + 17
  const modules: boolean[][] = Array.from({ length: size }, () => new Array(size).fill(false))
  const isFunction: boolean[][] = Array.from({ length: size }, () => new Array(size).fill(false))
  const setFn = (x: number, y: number, dark: boolean) => { modules[y][x] = dark; isFunction[y][x] = true }

  for (let i = 0; i < size; i++) { setFn(6, i, i % 2 === 0); setFn(i, 6, i % 2 === 0) }
  const finder = (cx: number, cy: number) => {
    for (let dy = -4; dy <= 4; dy++) for (let dx = -4; dx <= 4; dx++) {
      const dist = Math.max(Math.abs(dx), Math.abs(dy))
      const x = cx + dx; const y = cy + dy
      if (x >= 0 && x < size && y >= 0 && y < size) setFn(x, y, dist !== 2 && dist !== 4)
    }
  }
  finder(3, 3); finder(size - 4, 3); finder(3, size - 4)
  const pos = alignPositions(ver)
  pos.forEach((cx, i) => pos.forEach((cy, j) => {
    if ((i === 0 && j === 0) || (i === 0 && j === pos.length - 1) || (i === pos.length - 1 && j === 0)) return
    for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) setFn(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1)
  }))

  const drawFormat = (mask: number) => {
    const fmt = (0 << 3) | mask // error correction level M is 0
    let rem = fmt
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
    const f = ((fmt << 10) | rem) ^ 0x5412
    const bit = (i: number) => ((f >>> i) & 1) !== 0
    for (let i = 0; i <= 5; i++) setFn(8, i, bit(i))
    setFn(8, 7, bit(6)); setFn(8, 8, bit(7)); setFn(7, 8, bit(8))
    for (let i = 9; i < 15; i++) setFn(14 - i, 8, bit(i))
    for (let i = 0; i < 8; i++) setFn(size - 1 - i, 8, bit(i))
    for (let i = 8; i < 15; i++) setFn(8, size - 15 + i, bit(i))
    setFn(8, size - 8, true)
  }
  drawFormat(0)
  if (ver >= 7) {
    let rem = ver
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
    const v = (ver << 12) | rem
    for (let i = 0; i < 18; i++) {
      const dark = ((v >>> i) & 1) !== 0
      const a = size - 11 + (i % 3); const b = Math.floor(i / 3)
      setFn(a, b, dark); setFn(b, a, dark)
    }
  }

  // Codewords, zig-zag from the bottom right.
  let bitIndex = 0
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j
        const upward = ((right + 1) & 2) === 0
        const y = upward ? size - 1 - vert : vert
        if (!isFunction[y][x] && bitIndex < codewords.length * 8) {
          modules[y][x] = ((codewords[bitIndex >>> 3] >>> (7 - (bitIndex & 7))) & 1) !== 0
          bitIndex++
        }
      }
    }
  }

  // Choose the mask with the lowest penalty.
  const maskFn = [
    (x: number, y: number) => (x + y) % 2 === 0,
    (_x: number, y: number) => y % 2 === 0,
    (x: number) => x % 3 === 0,
    (x: number, y: number) => (x + y) % 3 === 0,
    (x: number, y: number) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0,
    (x: number, y: number) => ((x * y) % 2) + ((x * y) % 3) === 0,
    (x: number, y: number) => (((x * y) % 2) + ((x * y) % 3)) % 2 === 0,
    (x: number, y: number) => (((x + y) % 2) + ((x * y) % 3)) % 2 === 0,
  ]
  const applyMask = (grid: boolean[][], mask: number) => {
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
      if (!isFunction[y][x] && maskFn[mask](x, y)) grid[y][x] = !grid[y][x]
    }
  }
  const penalty = (g: boolean[][]): number => {
    let score = 0
    const line = (get: (i: number) => boolean) => {
      let run = 1
      for (let i = 1; i < size; i++) {
        if (get(i) === get(i - 1)) { run++; if (run === 5) score += 3; else if (run > 5) score++ } else run = 1
      }
    }
    for (let a = 0; a < size; a++) { line(i => g[a][i]); line(i => g[i][a]) }
    for (let y = 0; y < size - 1; y++) for (let x = 0; x < size - 1; x++) {
      if (g[y][x] === g[y][x + 1] && g[y][x] === g[y + 1][x] && g[y][x] === g[y + 1][x + 1]) score += 3
    }
    let dark = 0
    for (const row of g) for (const m of row) if (m) dark++
    score += Math.floor(Math.abs(dark * 20 - size * size * 10) / (size * size)) * 10
    return score
  }
  let best = 0; let bestScore = Infinity
  for (let mask = 0; mask < 8; mask++) {
    const trial = modules.map(r => r.slice())
    applyMask(trial, mask)
    const s = penalty(trial)
    if (s < bestScore) { bestScore = s; best = mask }
  }
  applyMask(modules, best)
  drawFormat(best)
  return modules
}

/** SVG path data drawing every dark module as a 1x1 square, offset by the quiet zone. */
export function qrPath(modules: boolean[][], quiet = 4): string {
  let d = ''
  modules.forEach((row, y) => row.forEach((dark, x) => { if (dark) d += `M${x + quiet} ${y + quiet}h1v1h-1z` }))
  return d
}
