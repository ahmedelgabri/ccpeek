import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
	testDir: './tests/e2e-go',
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 2 : 0,
	webServer: {
		command: './claude-history --skip-index --port 4322',
		port: 4322,
		reuseExistingServer: !process.env.CI,
	},
	projects: [
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'] },
		},
	],
})
