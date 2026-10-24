/** Strength of a password as the number of filled meter segments, 0..4. */
export function passwordStrength(pw: string): 0 | 1 | 2 | 3 | 4 {
  const hasLetter = /\p{L}/u.test(pw)
  const hasDigit = /\d/.test(pw)
  const hasSymbol = /[^\p{L}\d\s]/u.test(pw)
  const mixedCase = /\p{Ll}/u.test(pw) && /\p{Lu}/u.test(pw)
  const checks = [pw.length >= 8, pw.length >= 12, hasLetter && hasDigit, hasSymbol || mixedCase]
  return checks.filter(Boolean).length as 0 | 1 | 2 | 3 | 4
}

export function strengthLabel(n: number): string {
  if (n <= 1) return 'Слабый'
  if (n <= 3) return 'Средний'
  return 'Надёжный'
}
