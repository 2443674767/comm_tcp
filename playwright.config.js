import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: './tests', workers: 1, timeout: 45000,
  use: { baseURL: 'http://127.0.0.1:18096', trace: 'retain-on-failure', channel:process.env.PW_CHANNEL,
    launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', '--autoplay-policy=no-user-gesture-required'] } },
  webServer: {
    command: 'go run ./cmd/lanchat', url: 'http://127.0.0.1:18096/healthz', timeout: 120000,
    env: { DEV_HTTP: '1', LISTEN_ADDR: '127.0.0.1:18096', VOICE_UDP_ADDR: '127.0.0.1:18097', VOICE_TCP_ADDR: '127.0.0.1:18095', DATA_DIR: '.test-data' }
  }
});
