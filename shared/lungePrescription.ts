const quantity = String.raw`\d+(?:\.\d+)?(?:/\d+(?:\.\d+)?)?`;
const distance = String.raw`${quantity}\s*(?:feet|foot|ft|yards?|yds?|meters?|metres?|m|미터|피트|야드)(?![a-z])`;
const distanceBefore = new RegExp(String.raw`(?:^|[^a-z0-9.])(${distance})\s*[)\]]?\s*[:(]?\s*$`, "i");
const distanceAfter = new RegExp(String.raw`^\s*[:(]?\s*(${distance})(?=$|[^a-z0-9.])`, "i");
const countBefore = new RegExp(String.raw`(?:^|[^a-z0-9.])${quantity}\s*(?:reps?|회|개)?\s*[)\]]?\s*[:(]?\s*$`, "i");
const countAfter = new RegExp(String.raw`^\s*[:(]?\s*${quantity}\s*(?:reps?|회|개)?(?![a-z0-9가-힣./])`, "i");
const timeHeader = /\b(?:amrap|emom|e\d+mom|otm|every|time\s*cap|cap|rest|in|for)\s*[:\-]?\s*$/i;

// Match the whole movement so a distance before "DB Overhead Lunge" is
// associated with that lunge, rather than a later bare "Lunge" substring.
const englishLunge = String.raw`(?:(?:db|dumbbells?|kb|kettlebells?|barbell|sandbag|weighted)[\s-]+)?(?:walking[\s-]+)?(?:(?:overhead|front[\s-]*rack|back[\s-]*rack|farmer'?s?|goblet)[\s-]+)?(?:(?:walking|reverse|step[\s-]*back|jumping)[\s-]+)?lunges?`;
const koreanLunge = String.raw`(?:(?:덤벨|케틀벨|바벨|샌드백|중량)\s*)?(?:워킹\s*)?(?:(?:오버헤드|프[론런]트\s*랙|백\s*랙|파머스|고블릿)\s*)?(?:(?:워킹|리버스|점핑)\s*)?런지`;
const lunges = new RegExp(String.raw`(^|[^a-z0-9가-힣])(${englishLunge}|${koreanLunge})(?=$|[^a-z0-9가-힣])`, "gi");
const stationaryOrWalking = /\b(?:walking|reverse|step[\s-]*back|jumping)\b|워킹|리버스|점핑/i;
const prescriptionBoundary = /(?:^|[\r\n,;:+|·•→>\[/])\s*$/;
const headerBeforePrescription = /\b(?:\d+\s*rounds?(?:\s*for\s*time)?|for\s*time|(?:amrap|emom|e\d+mom|otm|every|time\s*cap|rest)\s*:?\s*\d+(?:\.\d+)?\s*(?:m|min(?:ute)?s?|분))\s*$/i;

/** Promote only the lunge with an adjacent distance prescription; preserve text and ratios. */
export function normalizeDistanceLungeClauses(text: string): string {
  return text.replace(lunges, (match, boundary: string, movement: string, offset: number) => {
    if (stationaryOrWalking.test(movement)) return match;
    const before = text.slice(0, offset + boundary.length);
    const after = text.slice(offset + match.length);
    if (/^\s*(?:stretch\b|스트레칭)/i.test(after)) return match;
    if (countBefore.test(before) || countAfter.test(after)) return match;

    const leading = distanceBefore.exec(before);
    const trailing = distanceAfter.exec(after);
    // WOD minute headers are not distances, even without punctuation or a newline.
    const prefix = leading == null ? "" : before.slice(0, leading.index + leading[0].indexOf(leading[1])).replace(/[(\[]\s*$/, "");
    const suffix = trailing == null ? "" : after.slice(trailing[0].length).replace(/^\s*[)\]]/, "");
    // A token between two movement names has ambiguous ownership. Prefer no
    // promotion to treating e.g. "Run 400m DB Lunge" as a distance lunge.
    const leadingIsDistance = leading != null && !timeHeader.test(prefix) &&
      (prescriptionBoundary.test(prefix) || headerBeforePrescription.test(prefix) || /\b(?:and|then)\s*$/i.test(prefix));
    const trailingIsDistance = trailing != null &&
      (/^\s*(?:$|[\r\n,;:+|·•→>/(\[])/.test(suffix) || /^\s*(?:and|then)\b/i.test(suffix));
    if (!leadingIsDistance && !trailingIsDistance) return match;
    return boundary + movement.replace(/-/g, " ").replace(/\blunges?\b/i, word => `walking ${word}`).replace(/런지/, "워킹 런지");
  });
}
