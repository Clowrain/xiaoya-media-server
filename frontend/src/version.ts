export const APP_VERSION = import.meta.env.VITE_APP_VERSION || 'v2.1.0';
export const BUILD_COMMIT = import.meta.env.VITE_BUILD_COMMIT || 'dev';
export const BUILD_TIME = import.meta.env.VITE_BUILD_TIME || '2026-11-08';

export const GITHUB_REPO_URL = 'https://github.com/Clowrain/xiaoya-media-server';

export const formatCommitHash = (commit?: string) => {
  if (!commit || commit === 'dev' || commit === 'unknown') return commit || 'dev';
  return commit.length > 8 ? commit.slice(0, 8) : commit;
};

export const getCommitUrl = (commit?: string) => {
  if (!commit || commit === 'dev' || commit === 'unknown') return GITHUB_REPO_URL;
  return `${GITHUB_REPO_URL}/commit/${commit}`;
};
