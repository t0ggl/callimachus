export const MAX_COVER_BYTES = 10 * 1024 * 1024; // 10 MiB
export const MIN_COVER_DIMENSION = 200;
export const MAX_COVER_DIMENSION = 4096;
export const MIN_COVER_ASPECT_RATIO = 0.5;
export const MAX_COVER_ASPECT_RATIO = 1.25;

const ALLOWED_COVER_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp']);
const ALLOWED_COVER_EXTENSIONS = ['.jpg', '.jpeg', '.png', '.webp'];

function hasAllowedExtension(name: string): boolean {
  const lower = name.toLowerCase();
  return ALLOWED_COVER_EXTENSIONS.some((ext) => lower.endsWith(ext));
}

async function readImageDimensions(file: File): Promise<{ width: number; height: number }> {
  if (typeof globalThis.createImageBitmap === 'function') {
    const bitmap = await globalThis.createImageBitmap(file);
    try {
      return { width: bitmap.width, height: bitmap.height };
    } finally {
      if (typeof bitmap.close === 'function') {
        bitmap.close();
      }
    }
  }

  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      URL.revokeObjectURL(url);
      resolve({ width: img.naturalWidth || img.width, height: img.naturalHeight || img.height });
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error('Image decode failed'));
    };
    img.src = url;
  });
}

export async function validateCoverFile(file: File): Promise<string | null> {
  if (file.size === 0) {
    return 'Cover image file is empty.';
  }
  if (file.size > MAX_COVER_BYTES) {
    return 'Cover image must be 10 MB or smaller.';
  }
  const type = file.type.toLowerCase();
  if (type ? !ALLOWED_COVER_TYPES.has(type) : !hasAllowedExtension(file.name)) {
    return 'Cover image must be a valid JPEG, PNG, or WebP file.';
  }

  let width: number;
  let height: number;
  try {
    const dims = await readImageDimensions(file);
    width = dims.width;
    height = dims.height;
  } catch {
    return 'Cover image is corrupted or unreadable. Choose a valid JPEG, PNG, or WebP file.';
  }

  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return 'Cover image is corrupted or unreadable. Choose a valid JPEG, PNG, or WebP file.';
  }
  if (width < MIN_COVER_DIMENSION || height < MIN_COVER_DIMENSION) {
    return `Cover image is too small (${width}×${height} px). Minimum dimensions are ${MIN_COVER_DIMENSION}×${MIN_COVER_DIMENSION} px.`;
  }
  if (width > MAX_COVER_DIMENSION || height > MAX_COVER_DIMENSION) {
    return `Cover image is too large (${width}×${height} px). Maximum dimensions are ${MAX_COVER_DIMENSION}×${MAX_COVER_DIMENSION} px.`;
  }
  const ratio = width / height;
  if (ratio < MIN_COVER_ASPECT_RATIO || ratio > MAX_COVER_ASPECT_RATIO) {
    return `Cover aspect ratio (${ratio.toFixed(2)}:1) is outside the supported book cover range (${MIN_COVER_ASPECT_RATIO.toFixed(2)}:1 to ${MAX_COVER_ASPECT_RATIO.toFixed(2)}:1, portrait to square).`;
  }
  return null;
}
