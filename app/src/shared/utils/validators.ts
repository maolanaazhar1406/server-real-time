export function isValidEmail(email: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim());
}

export function isValidUsername(username: string): boolean {
  return /^[a-zA-Z0-9_-]{3,24}$/.test(username.trim());
}

export function isValidPassword(password: string): boolean {
  return password.length >= 6;
}

export function slugifyTopic(input: string): string {
  return input
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-_]+/g, '-')
    .replace(/^-+|-+$/g, '');
}
