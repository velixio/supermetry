const version = 6;
const size = version * 4 + 17;
const dataCodewords = 136;
const blockDataCodewords = 68;
const errorCodewords = 18;

function appendBits(target: boolean[], value: number, length: number) {
  for (let index = length - 1; index >= 0; index -= 1)
    target.push(((value >>> index) & 1) !== 0);
}

function multiply(left: number, right: number): number {
  let result = 0;
  for (let index = 0; index < 8; index += 1) {
    result = (result << 1) ^ ((result >>> 7) * 0x11d);
    result ^= ((right >>> (7 - index)) & 1) * left;
  }
  return result;
}

function divisor(degree: number): Uint8Array {
  const result = new Uint8Array(degree);
  result[degree - 1] = 1;
  let root = 1;
  for (let index = 0; index < degree; index += 1) {
    for (let offset = 0; offset < degree; offset += 1) {
      result[offset] = multiply(result[offset]!, root);
      if (offset + 1 < degree) result[offset] ^= result[offset + 1]!;
    }
    root = multiply(root, 2);
  }
  return result;
}

function remainder(data: Uint8Array, polynomial: Uint8Array): Uint8Array {
  const result = new Uint8Array(polynomial.length);
  for (const byte of data) {
    const factor = byte ^ result[0]!;
    result.copyWithin(0, 1);
    result[result.length - 1] = 0;
    for (let index = 0; index < result.length; index += 1) {
      result[index] ^= multiply(polynomial[index]!, factor);
    }
  }
  return result;
}

function payload(value: string): boolean[] {
  const bytes = new TextEncoder().encode(value);
  if (bytes.length > 134)
    throw new Error("The authenticator URL is too long to encode");
  const bits: boolean[] = [];
  appendBits(bits, 0b0100, 4);
  appendBits(bits, bytes.length, 8);
  for (const byte of bytes) appendBits(bits, byte, 8);
  appendBits(bits, 0, Math.min(4, dataCodewords * 8 - bits.length));
  while (bits.length % 8 !== 0) bits.push(false);
  let pad = 0xec;
  while (bits.length < dataCodewords * 8) {
    appendBits(bits, pad, 8);
    pad ^= 0xfd;
  }
  const data = Uint8Array.from(
    Array.from({ length: dataCodewords }, (_, index) =>
      bits
        .slice(index * 8, index * 8 + 8)
        .reduce((byte, bit) => (byte << 1) | Number(bit), 0),
    ),
  );
  const blocks = [
    data.slice(0, blockDataCodewords),
    data.slice(blockDataCodewords),
  ];
  const polynomial = divisor(errorCodewords);
  const checks = blocks.map((block) => remainder(block, polynomial));
  const codewords: number[] = [];
  for (let index = 0; index < blockDataCodewords; index += 1) {
    for (const block of blocks) codewords.push(block[index]!);
  }
  for (let index = 0; index < errorCodewords; index += 1) {
    for (const check of checks) codewords.push(check[index]!);
  }
  const result: boolean[] = [];
  for (const byte of codewords) appendBits(result, byte, 8);
  return result;
}

function formatBits(): number {
  const data = 0b01 << 3;
  let value = data << 10;
  for (let bit = 14; bit >= 10; bit -= 1) {
    if (((value >>> bit) & 1) !== 0) value ^= 0x537 << (bit - 10);
  }
  return ((data << 10) | value) ^ 0x5412;
}

function setModule(
  matrix: boolean[][],
  reserved: boolean[][],
  x: number,
  y: number,
  dark: boolean,
) {
  if (x < 0 || y < 0 || x >= size || y >= size) return;
  matrix[y]![x] = dark;
  reserved[y]![x] = true;
}

function drawFinder(
  matrix: boolean[][],
  reserved: boolean[][],
  centerX: number,
  centerY: number,
) {
  for (let y = -4; y <= 4; y += 1) {
    for (let x = -4; x <= 4; x += 1) {
      const distance = Math.max(Math.abs(x), Math.abs(y));
      setModule(
        matrix,
        reserved,
        centerX + x,
        centerY + y,
        distance !== 2 && distance !== 4,
      );
    }
  }
}

function drawAlignment(
  matrix: boolean[][],
  reserved: boolean[][],
  centerX: number,
  centerY: number,
) {
  for (let y = -2; y <= 2; y += 1) {
    for (let x = -2; x <= 2; x += 1) {
      setModule(
        matrix,
        reserved,
        centerX + x,
        centerY + y,
        Math.max(Math.abs(x), Math.abs(y)) !== 1,
      );
    }
  }
}

/** Encodes a Version 6-L QR matrix locally; no TOTP secret leaves the browser. */
export function encodeQrMatrix(value: string): boolean[][] {
  const matrix = Array.from({ length: size }, () =>
    Array.from({ length: size }, () => false),
  );
  const reserved = Array.from({ length: size }, () =>
    Array.from({ length: size }, () => false),
  );
  drawFinder(matrix, reserved, 3, 3);
  drawFinder(matrix, reserved, size - 4, 3);
  drawFinder(matrix, reserved, 3, size - 4);
  drawAlignment(matrix, reserved, size - 7, size - 7);
  for (let index = 8; index < size - 8; index += 1) {
    setModule(matrix, reserved, 6, index, index % 2 === 0);
    setModule(matrix, reserved, index, 6, index % 2 === 0);
  }
  for (let index = 0; index < 9; index += 1) {
    if (index !== 6) {
      setModule(matrix, reserved, 8, index, false);
      setModule(matrix, reserved, index, 8, false);
    }
  }
  for (let index = 0; index < 8; index += 1) {
    setModule(matrix, reserved, size - 1 - index, 8, false);
    setModule(matrix, reserved, 8, size - 1 - index, false);
  }
  setModule(matrix, reserved, 8, size - 8, true);

  const bits = payload(value);
  let bitIndex = 0;
  let upward = true;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right -= 1;
    for (let offset = 0; offset < size; offset += 1) {
      const y = upward ? size - 1 - offset : offset;
      for (let column = 0; column < 2; column += 1) {
        const x = right - column;
        if (reserved[y]![x]) continue;
        matrix[y]![x] = (bits[bitIndex] ?? false) !== ((x + y) % 2 === 0);
        bitIndex += 1;
      }
    }
    upward = !upward;
  }

  const format = formatBits();
  const formatBit = (index: number) => ((format >>> index) & 1) !== 0;
  for (let index = 0; index <= 5; index += 1)
    matrix[index]![8] = formatBit(index);
  matrix[7]![8] = formatBit(6);
  matrix[8]![8] = formatBit(7);
  matrix[8]![7] = formatBit(8);
  for (let index = 9; index < 15; index += 1)
    matrix[8]![14 - index] = formatBit(index);
  for (let index = 0; index < 8; index += 1)
    matrix[8]![size - 1 - index] = formatBit(index);
  for (let index = 8; index < 15; index += 1)
    matrix[size - 15 + index]![8] = formatBit(index);
  return matrix;
}

export function TotpQr({ uri }: { uri: string }) {
  const matrix = encodeQrMatrix(uri);
  return (
    <svg
      aria-label="Authenticator setup QR code"
      className="size-48 bg-white p-2"
      role="img"
      viewBox={`-4 -4 ${size + 8} ${size + 8}`}
      xmlns="http://www.w3.org/2000/svg"
    >
      <rect x="-4" y="-4" width={size + 8} height={size + 8} fill="white" />
      {matrix.flatMap((row, y) =>
        row.map((filled, x) =>
          filled ? (
            <rect
              key={`${x}:${y}`}
              x={x}
              y={y}
              width="1"
              height="1"
              fill="black"
            />
          ) : null,
        ),
      )}
    </svg>
  );
}
