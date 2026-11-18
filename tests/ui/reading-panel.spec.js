const { test, expect } = require('@playwright/test');

test('reading panel opens articles and comments, changes layout, and closes', async ({ page, context }, testInfo) => {
  let settings = {
    refresh_interval_min: 15, max_articles_per_feed: 500,
    default_display_mode: 'headline', default_sort_order: 'desc',
    auto_refresh_enabled: false, release_check_enabled: false,
    link_open_target: 'panel', reading_panel_layout: 'right',
  };
  await page.route('**/api/settings', async route => {
    if (route.request().method() === 'POST') {
      settings = { ...settings, ...Object.fromEntries(new URLSearchParams(route.request().postData())) };
    }
    await route.fulfill({ json: settings });
  });
  await page.route('**/api/groups', route => route.fulfill({ json: [{ id: 1, name: 'Panel test', feed_count: 1, unread_count: 1 }] }));
  await page.route('**/api/feeds', route => route.fulfill({ json: [{ id: 11, group_id: 1, title: 'Test feed', unread_count: 1 }] }));
  await page.route('**/api/articles?**', route => route.fulfill({ json: {
    articles: [{ id: 1, feed_id: 11, feed_title: 'Test feed', title: 'Panel article', link: 'https://panel.example/article', comments_link: 'https://panel.example/comments', description: '<p>Article summary. <a href="https://panel.example/related">Related link</a> <a href="https://news.ycombinator.com/item?id=100">Hacker News discussion</a></p>', is_read: false }],
    total: 1, has_more: false,
  } }));
  await page.route('**/api/articles/read', route => route.fulfill({ json: { ok: true } }));
  await context.route('https://panel.example/**', route => route.fulfill({
    contentType: 'text/html',
    headers: route.request().url().endsWith('/comments') ? { 'X-Frame-Options': 'DENY' } : {},
    body: '<h1>Embedded page</h1><p>Loaded successfully.</p>',
  }));
  const hnItems = {
    100: { id: 100, type: 'story', kids: [101, 102] },
    101: { id: 101, type: 'comment', by: 'hn-reader', text: 'A <em>top-level</em> comment <script>window.untrustedRan = true</script>', kids: [103] },
    102: { id: 102, type: 'comment', deleted: true, text: 'Deleted text' },
    103: { id: 103, type: 'comment', by: 'reply-author', text: 'A reply <a href="javascript:alert(1)">Unsafe link</a>' },
  };
  await context.route('https://hacker-news.firebaseio.com/v0/item/*.json', route => {
    const id = new URL(route.request().url()).pathname.split('/').pop().replace('.json', '');
    return route.fulfill({ json: hnItems[id] ?? null });
  });
  await page.goto('/');
  if (new URL(page.url()).pathname === '/login') {
    const setup = await page.getByRole('button', { name: 'Create administrator', exact: true }).isVisible();
    await page.getByLabel('Username').fill('admin');
    await page.getByLabel('Password').fill('admin123');
    await Promise.all([page.waitForURL('/'), page.getByRole('button', { name: setup ? 'Create administrator' : 'Sign in', exact: true }).click()]);
  }
  const title = page.locator('.article-title').first();
  await expect(title).toBeVisible();
  await title.click();
  const panel = page.locator('#reading-panel');
  const frame = page.locator('#reading-panel-frame');
  await expect(panel).toBeVisible();
  await expect(frame).toHaveAttribute('src', 'https://panel.example/article');
  await expect(page.frameLocator('#reading-panel-frame').getByRole('heading')).toHaveText('Embedded page');
  expect(page.context().pages()).toHaveLength(1);
  const itemsBox = await page.locator('#article-pane').boundingBox();
  const panelBox = await panel.boundingBox();
  if (testInfo.project.name === 'desktop') {
    expect(panelBox.x).toBeCloseTo(itemsBox.x + itemsBox.width, 0);
    expect(panelBox.y).toBeCloseTo(itemsBox.y, 0);
  } else {
    expect(panelBox.y).toBeCloseTo(itemsBox.y + itemsBox.height, 0);
  }
  await page.screenshot({ path: testInfo.outputPath('panel-right.png') });
  const divider = page.getByRole('separator', { name: 'Resize reading panel' });
  const resizeKey = testInfo.project.name === 'desktop' ? 'ArrowLeft' : 'ArrowUp';
  if (testInfo.project.name === 'desktop') {
    const grip = await divider.boundingBox();
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await page.mouse.down();
    await page.mouse.move(grip.x + grip.width / 2 - itemsBox.width / 5, grip.y + grip.height / 2, { steps: 5 });
    await page.mouse.up();
    await expect(divider).toHaveAttribute('aria-valuetext', 'Reading panel: 60%');
  } else {
    await divider.press(resizeKey);
    await expect(divider).toHaveAttribute('aria-valuetext', 'Reading panel: 55%');
  }
  const resizedPercent = testInfo.project.name === 'desktop' ? 60 : 55;
  await title.focus();
  await page.keyboard.press(`Shift+${resizeKey}`);
  await expect(divider).toHaveAttribute('aria-valuetext', `Reading panel: ${resizedPercent + 5}%`);
  await page.keyboard.press(testInfo.project.name === 'desktop' ? 'Shift+ArrowRight' : 'Shift+ArrowDown');
  await expect(divider).toHaveAttribute('aria-valuetext', `Reading panel: ${resizedPercent}%`);

  // The comments page refuses framing, as some real feeds' discussion pages do.
  const blocked = page.waitForEvent('console', { predicate: message => message.text().includes('X-Frame-Options') });
  await page.keyboard.press('c');
  await expect(frame).toHaveAttribute('src', 'https://panel.example/comments');
  await blocked;
  const externalTabPromise = page.waitForEvent('popup');
  await page.getByRole('link', { name: 'Open in new tab' }).click();
  await expect(panel).toBeHidden();
  const externalTab = await externalTabPromise;
  await expect(externalTab).toHaveURL('https://panel.example/comments');
  await externalTab.close();
  await page.bringToFront();
  await expect(frame).not.toHaveAttribute('src');
  await page.keyboard.press('v');
  await expect(divider).toHaveAttribute('aria-valuetext', `Reading panel: ${resizedPercent}%`);
  await page.keyboard.press('x');
  await expect(panel).toBeHidden();
  await page.keyboard.press('v');
  await expect(panel).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(panel).toBeHidden();
  // Expand the summary, then exercise its ordinary and comment anchors.
  await page.locator('.article-meta').first().click();
  await page.getByRole('link', { name: 'Comments', exact: true }).click();
  await expect(frame).toHaveAttribute('src', 'https://panel.example/comments');
  await page.getByRole('link', { name: 'Related link' }).click();
  await expect(frame).toHaveAttribute('src', 'https://panel.example/related');
  await page.getByRole('link', { name: 'Hacker News discussion' }).click();
  const discussion = page.getByRole('region', { name: 'Hacker News discussion' });
  await expect(discussion.getByText('Hacker News · 3 comments')).toBeVisible();
  await expect(discussion.locator('.hn-replies')).toContainText('reply-author');
  await expect(discussion.locator('em')).toHaveText('top-level');
  await expect(discussion.getByText('[deleted]', { exact: true })).toBeVisible();
  await expect(discussion.getByText('Deleted text')).toHaveCount(0);
  await expect(discussion.locator('script, [href^="javascript:"]')).toHaveCount(0);
  await expect(frame).toBeHidden();
  await expect(panel.getByRole('link', { name: 'Open in new tab' })).toHaveAttribute('href', 'https://news.ycombinator.com/item?id=100');
  await page.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(panel).toBeHidden();

  async function openSettings() {
    const actions = page.getByRole('button', { name: 'Open actions', exact: true });
    if (await actions.isVisible()) await actions.click();
    await page.getByRole('button', { name: 'Settings', exact: true }).click();
  }
  await openSettings();
  await expect(page.locator('#app-version')).toHaveText('feedss dev');
  await page.getByLabel('Reading panel position').selectOption('bottom');
  await page.getByRole('button', { name: 'Save settings', exact: true }).click();
  await expect(page.locator('#settings-modal')).not.toBeVisible();
  await expect(page.locator('#save-settings-btn')).toBeEnabled();
  await page.keyboard.press('v');
  await expect(panel).toBeVisible();
  await expect(page.locator('#reading-workspace')).toHaveAttribute('data-layout', 'bottom');
  const bottomItems = await page.locator('#article-pane').boundingBox();
  const bottomPanel = await panel.boundingBox();
  expect(bottomPanel.y).toBeCloseTo(bottomItems.y + bottomItems.height, 0);
  const bottomPercent = testInfo.project.name === 'desktop' ? 50 : 55;
  expect(100 * bottomPanel.height / (bottomPanel.height + bottomItems.height)).toBeCloseTo(bottomPercent, 0);
  await divider.press('ArrowUp');
  await expect(divider).toHaveAttribute('aria-valuetext', `Reading panel: ${bottomPercent + 5}%`);
  await page.reload();
  await expect(title).toBeVisible();
  await title.click();
  await expect(divider).toHaveAttribute('aria-valuetext', `Reading panel: ${bottomPercent + 5}%`);
  await expect(page.locator('#article-pane')).not.toHaveCSS('outline-style', 'auto');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('panel-bottom.png') });
  await openSettings();
  await page.getByLabel('Open article and comment links in').selectOption('tab');
  await page.getByRole('button', { name: 'Save settings', exact: true }).click();
  await expect(page.locator('#settings-modal')).not.toBeVisible();
  await expect(page.locator('#save-settings-btn')).toBeEnabled();
  await expect(panel).toBeHidden();
  const popupPromise = page.waitForEvent('popup');
  await page.keyboard.press('c');
  const popup = await popupPromise;
  await expect(popup).toHaveURL('https://panel.example/comments');
  await popup.close();
  await page.reload();
  await openSettings();
  await expect(page.getByLabel('Open article and comment links in')).toHaveValue('tab');
  await expect(page.getByLabel('Reading panel position')).toHaveValue('bottom');
});
