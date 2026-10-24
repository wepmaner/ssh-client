import { describe, expect, it } from 'vitest'
import { hostsLabel } from './format'

describe('hostsLabel', () => {
  it('declines хост', () => {
    expect([0, 1, 2, 5, 11, 12, 21, 22, 25].map(hostsLabel)).toEqual([
      '0 хостов',
      '1 хост',
      '2 хоста',
      '5 хостов',
      '11 хостов',
      '12 хостов',
      '21 хост',
      '22 хоста',
      '25 хостов',
    ])
  })
})
