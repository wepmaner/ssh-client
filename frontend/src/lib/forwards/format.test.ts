import { describe, expect, it } from 'vitest'
import { shortListen } from './format'

describe('shortListen', () => {
  it('drops the default 127.0.0.1 and keeps everything else', () => {
    expect(shortListen('127.0.0.1:5432')).toBe('5432')
    expect(shortListen('0.0.0.0:9000')).toBe('0.0.0.0:9000')
    expect(shortListen('[::1]:80')).toBe('[::1]:80')
    expect(shortListen('5432')).toBe('5432')
  })
})
