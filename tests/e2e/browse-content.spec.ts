import { test, expect } from '@playwright/test'

test.describe('browse plans', () => {
	test('lists plans and can view a plan', async ({ page }) => {
		await page.goto('/plans/')
		const main = page.locator('main')
		await expect(main.getByRole('heading', { name: 'Plans' })).toBeVisible()

		// Click on first plan link (scoped to main to avoid nav links)
		const firstLink = main.locator('a').first()
		await firstLink.click()

		// Verify we're on a plan detail page with rendered markdown
		await expect(main.locator('.prose')).toBeVisible()
	})
})

test.describe('browse shell snapshots', () => {
	test('lists snapshots and can view one', async ({ page }) => {
		await page.goto('/shell-snapshots/')
		const main = page.locator('main')
		await expect(main.getByRole('heading', { name: 'Shell Snapshots' })).toBeVisible()

		const firstLink = main.locator('a').first()
		await firstLink.click()

		// Shiki renders code blocks
		await expect(main.locator('pre')).toBeVisible()
	})
})

test.describe('browse todos', () => {
	test('lists todos with status badges', async ({ page }) => {
		await page.goto('/todos/')
		const main = page.locator('main')
		await expect(main.getByRole('heading', { name: 'Todos' })).toBeVisible()

		const firstLink = main.locator('a').first()
		await firstLink.click()

		// Verify todo items render (React island)
		await expect(main.locator('ul')).toBeVisible()
	})
})

test.describe('browse projects', () => {
	test('lists projects and can navigate to sessions', async ({ page }) => {
		await page.goto('/projects/')
		const main = page.locator('main')
		await expect(main.getByRole('heading', { name: 'Projects' })).toBeVisible()

		// Click on first project
		const firstProject = main.locator('a').first()
		await firstProject.click()

		// Verify sessions heading
		await expect(main.getByText('sessions').first()).toBeVisible()
	})

	test('can view a conversation', async ({ page }) => {
		await page.goto('/projects/')
		const main = page.locator('main')

		// Navigate to first project
		await main.locator('a').first().click()

		// Wait for React hydration of the sessions list
		await main.getByPlaceholder('Filter sessions...').waitFor({ timeout: 10000 })

		// Click on a session link (contains "msgs" text)
		const sessionLink = main.locator('a').filter({ hasText: 'msgs' }).first()
		await sessionLink.click()

		// Verify conversation page loaded with message count
		await expect(main.getByText(/\d+ messages/).first()).toBeVisible()
	})
})

test.describe('browse file history', () => {
	test('lists file history entries', async ({ page }) => {
		await page.goto('/file-history/')
		const main = page.locator('main')
		await expect(main.getByRole('heading', { name: 'File History' })).toBeVisible()

		const firstLink = main.locator('a').first()
		await expect(firstLink).toBeVisible()
	})
})

test.describe('search', () => {
	test('plans search filters results', async ({ page }) => {
		await page.goto('/plans/')
		const main = page.locator('main')

		const searchInput = main.getByPlaceholder('Filter plans...')
		await expect(searchInput).toBeVisible()

		await searchInput.fill('implementation')

		const countText = main.getByText(/of \d+ items/)
		await expect(countText).toBeVisible()
	})

	test('projects search filters results', async ({ page }) => {
		await page.goto('/projects/')
		const main = page.locator('main')

		const searchInput = main.getByPlaceholder('Filter projects...')
		await expect(searchInput).toBeVisible()

		await searchInput.fill('dotfiles')

		const countText = main.getByText(/of \d+ items/)
		await expect(countText).toBeVisible()
	})
})
