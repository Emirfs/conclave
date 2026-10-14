import { describe, expect, test } from 'bun:test'
import { maskGrantText, parseSensitiveTerms } from './grantPrivacy'

describe('Grant sharing privacy', () => {
  test('masks identifiers while preserving project targets and budget', () => {
    const source = 'İletişim: person@example.com, +90 (532) 123 45 67. Kimlik: 12345678901. IBAN: TR33 0006 1005 1978 6457 8413 26. Bütçe: 2.400.000 TL, süre: 18 ay, hedef: %98.'
    const result = maskGrantText(source)
    expect(result.text).toBe('İletişim: [E-POSTA], [TELEFON]. Kimlik: [KİMLİK NUMARASI]. IBAN: [IBAN]. Bütçe: 2.400.000 TL, süre: 18 ay, hedef: %98.')
    expect(result.maskedCount).toBe(4)
  })

  test('removes credentials before identifiers can fragment them', () => {
    const source = 'password="secret@example.com"\nBearer abc12345678901\nhttps://user:secret@example.com/private\n-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----'
    const result = maskGrantText(source)
    expect(result.text).toBe('[GİZLİ BİLGİ]\n[KİMLİK DOĞRULAMA]\n[KİMLİK BİLGİLİ ADRES]\n[GİZLİ ANAHTAR]')
    expect(result.maskedCount).toBe(4)
  })

  test('treats custom sensitive phrases literally and masks longer phrases first', () => {
    const result = maskGrantText('Acme A.Ş. uses a+b. ACME A.Ş. owns the project.', ['Acme', 'Acme A.Ş.', 'a+b', 'Acme'])
    expect(result.text).toBe('[ÖZEL BİLGİ] uses [ÖZEL BİLGİ]. [ÖZEL BİLGİ] owns the project.')
    expect(result.maskedCount).toBe(3)
  })

  test('does not mutate source or custom terms and tolerates multiline terms', () => {
    const source = 'Ayşe\nProje: sensör kontrolü, 18 ay'
    const terms = parseSensitiveTerms('  Ayşe  \r\n\n')
    expect(maskGrantText(source, terms).text).toBe('[ÖZEL BİLGİ]\nProje: sensör kontrolü, 18 ay')
    expect(terms).toEqual(['Ayşe'])
    expect(source).toBe('Ayşe\nProje: sensör kontrolü, 18 ay')
  })
})
