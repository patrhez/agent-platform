const crockfordBase32 = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

export function newClientMessageID(): string {
  let timestamp = BigInt(Date.now());
  let identifier = "";
  for (let index = 0; index < 10; index += 1) {
    identifier = crockfordBase32[Number(timestamp % 32n)] + identifier;
    timestamp /= 32n;
  }

  const random = crypto.getRandomValues(new Uint8Array(16));
  for (const value of random) identifier += crockfordBase32[value & 31];
  return identifier;
}
