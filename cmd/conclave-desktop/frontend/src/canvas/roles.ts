/** Roles two cards can take when they work together.
 *
 *  A role is only ever text that goes into the briefing: it does not change a
 *  card's access or what it is allowed to decide. That keeps any provider
 *  usable in any position of a workflow, which is the point of running several
 *  of them side by side.
 */
export type Role = {
  /** Short name, shown on the chip and in the card's title bar. */
  name: string
  /** What the card is told it is there to do. */
  text: string
}

export const ROLES: Role[] = [
  {
    name: 'uygulayan',
    text: 'İşi fiilen yapan taraf sensin. Değişikliği uygula, çalıştır ve sonucu bildir. Ne yaptığını kısaca yaz, planı tekrar anlatma.',
  },
  {
    name: 'gözden geçiren',
    text: 'Karşındaki kartın çıktısını incele. Hata, eksik ve riskleri somut olarak yaz; sorun görmüyorsan kısaca onayla. Kendin uygulama, değerlendir.',
  },
  {
    name: 'mimar',
    text: 'Yaklaşımı ve yapıyı sen belirle. Ne yapılacağını ve neden öyle yapılacağını net biçimde tarif et; uygulamayı karşı tarafa bırak.',
  },
  {
    name: 'araştırmacı',
    text: 'Gereken bilgiyi topla ve doğrula. Bulduklarını kaynağıyla birlikte özetle; karar verme, karşı tarafın karar verebilmesi için zemin hazırla.',
  },
  {
    name: 'editör',
    text: 'Gelen metni yayına hazır hâle getir. Anlamı koru, gereksizi at, tutarsızlığı düzelt. Yeniden yazdığın hâli ver.',
  },
  {
    name: 'sorgulayan',
    text: 'Gelen öneriyi zorla. Varsayımları açığa çıkar, atlanmış durumları sor, en zayıf halkayı göster. Yıkmak için değil, sağlamlaştırmak için sorgula.',
  },
]

/** Complementary pairs, offered on a link because a role only means something
 *  next to the other card's role. */
export type RolePair = { label: string; source: Role; target: Role }

export const ROLE_PAIRS: RolePair[] = [
  { label: 'uygulayan ↔ gözden geçiren', source: role('uygulayan'), target: role('gözden geçiren') },
  { label: 'mimar ↔ uygulayan', source: role('mimar'), target: role('uygulayan') },
  { label: 'araştırmacı ↔ uygulayan', source: role('araştırmacı'), target: role('uygulayan') },
  { label: 'uygulayan ↔ sorgulayan', source: role('uygulayan'), target: role('sorgulayan') },
  { label: 'yazan ↔ editör', source: role('uygulayan'), target: role('editör') },
]

function role(name: string): Role {
  const found = ROLES.find((item) => item.name === name)
  if (!found) throw new Error(`unknown role: ${name}`)
  return found
}

/** The chip label for a role text: the template's name when the text is one of
 *  the templates, otherwise the first few words of whatever the user wrote. */
export function roleName(text: string): string {
  const trimmed = text.trim()
  if (trimmed === '') return ''
  const template = ROLES.find((item) => item.text === trimmed)
  if (template) return template.name
  const words = trimmed.split(/\s+/).slice(0, 3).join(' ')
  return words.length < trimmed.length ? `${words}…` : words
}
