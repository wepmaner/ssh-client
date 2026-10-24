import { describe, expect, it } from 'vitest'
import { passwordStrength } from './password'

describe('passwordStrength', () => {
  it.each([
    ['', 0],
    ['abc', 0],
    ['abcdefgh', 1],
    ['abcdefg1', 2],
    ['abcdefghijkl', 2],
    ['abcdefghijk1', 3],
    ['Abcdefghijk1', 4],
    ['abcdefg1!', 3],
  ])('%j gives %i', (pw, n) => {
    expect(passwordStrength(pw)).toBe(n)
  })
})
