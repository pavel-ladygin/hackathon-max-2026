import { expect, test } from '@playwright/test'

async function completeOnboarding(page: import('@playwright/test').Page) {
  await expect(page.getByRole('heading', { name: 'Что вам интересно?' })).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await page.getByRole('button', { name: 'Сформировать подборку' }).click()
  await expect(page.getByRole('heading', { name: 'Куда сходить вдвоём?' })).toBeVisible()
}

test('two participants reach the same match using the OpenAPI mock', async ({ page, context }) => {
  await page.goto('/?resetMock=1&mockUser=ivan')
  await completeOnboarding(page)
  await page.getByRole('button', { name: 'Создать комнату' }).click()
  await expect(page.getByRole('heading', { name: 'Создать комнату' })).toBeVisible()
  await page.getByRole('button', { name: 'Создать комнату' }).click()
  const inviteUrl = await page.getByTestId('invite-url').textContent()
  expect(inviteUrl).toContain('/join/')

  const anna = await context.newPage()
  await anna.goto(inviteUrl!)
  await anna.getByRole('button', { name: 'Присоединиться' }).click()
  await expect(anna.getByRole('heading', { name: 'Мои предпочтения' })).toBeVisible()

  await page.getByRole('button', { name: 'Перейти к моим пожеланиям' }).click()
  await page.getByRole('button', { name: 'Сохранить предпочтения' }).click()
  await anna.getByRole('button', { name: 'Сохранить предпочтения' }).click()

  await expect(page.getByText('Один и тот же пул, независимые оценки')).toBeVisible({ timeout: 10_000 })
  await expect(anna.getByText('Один и тот же пул, независимые оценки')).toBeVisible({ timeout: 10_000 })
  await page.getByRole('button', { name: /Хочу пойти/ }).click()
  await anna.getByRole('button', { name: /Хочу пойти/ }).click()
  await expect(page.getByText('Это мэтч!')).toBeVisible({ timeout: 10_000 })
  await expect(anna.getByText('Это мэтч!')).toBeVisible({ timeout: 10_000 })
})

test('catalog remains usable without geolocation', async ({ page }) => {
  await page.goto('/?resetMock=1&mockUser=ivan')
  await completeOnboarding(page)
  await page.goto('/events')
  await expect(page.getByRole('heading', { name: 'События' })).toBeVisible()
  await page.getByRole('searchbox', { name: 'Найти событие' }).fill('джаз')
  await expect(page.getByText('Джазовый вечер в Оранжерее')).toBeVisible()
})
