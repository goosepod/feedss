const { test, expect } = require('@playwright/test');

async function login(page) {
  await page.goto('/login');
	const setupHeading = page.getByRole('heading', { name: 'Create administrator', exact: true });
	const isFirstStart = await setupHeading.isVisible();
	if (isFirstStart) {
		await expect(page.getByText('The account you create here will become the administrator')).toBeVisible();
	} else {
		await expect(page.getByRole('heading', { name: 'Sign in', exact: true })).toBeVisible();
	}
	await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', '/static/favicon.svg');
	await expect(page.locator('.login-card')).toBeVisible();
	await expect(page.locator('.login-card')).toHaveCSS('display', 'grid');
	await expect(page.getByLabel('Username')).toHaveCSS('caret-color', 'rgb(24, 32, 42)');
	await expect(page.getByLabel('Username')).toHaveCSS('cursor', 'default');
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Password').fill('admin123');
  await Promise.all([
    page.waitForURL('/'),
		page.getByRole('button', { name: isFirstStart ? 'Create administrator' : 'Sign in', exact: true }).click(),
  ]);
  await expect(page.locator('#feed-header')).toBeVisible();
}

async function expectSelectedArticleToKeepTopPadding(page) {
	await expect.poll(async () => {
		const articleBox = await page.locator('.article.selected').boundingBox();
		const pane = page.locator('#article-pane');
		const paneBox = await pane.boundingBox();
		const articleGap = await page.locator('.article').nth(1).evaluate(element => parseFloat(getComputedStyle(element).marginTop));
		return Math.abs((articleBox.y - paneBox.y) - articleGap);
	}).toBeLessThan(3);
	const mask = await page.locator('#article-pane').evaluate(element => {
		const style = getComputedStyle(element, '::before');
		return {
			height: parseFloat(style.height),
			paddingTop: parseFloat(getComputedStyle(element).paddingTop),
			position: style.position,
			top: parseFloat(style.top),
			zIndex: style.zIndex,
		};
	});
	expect(mask.height).toBe(10);
	expect(mask.position).toBe('sticky');
	expect(mask.top).toBe(-mask.paddingTop);
	expect(Number(mask.zIndex)).toBeGreaterThan(0);
}

test('core reader workflow is usable', async ({ page }, testInfo) => {
	const users = [{ id: 1, username: 'admin', is_admin: true, must_change_password: false }];
	const groups = [
		{ id: 1, name: 'Programming', feed_count: 2, unread_count: 119 },
		{ id: 2, name: 'Board games', feed_count: 1, unread_count: 2 },
	].concat(Array.from({ length: 10 }, (_, index) => ({
		id: 100 + index, name: `Placeholder ${index + 1}`, feed_count: 0, unread_count: 0,
	})), [
		{ id: 50, name: 'Scroll test', feed_count: 1, unread_count: 0 },
	], Array.from({ length: 14 }, (_, index) => ({
		id: 110 + index, name: `Placeholder ${index + 11}`, feed_count: 0, unread_count: 0,
	})));
	const feeds = [
		{ id: 11, group_id: 1, title: 'Hacker News', url: 'https://news.ycombinator.com/rss', display_mode: 'headline', sort_direction: 'desc', unread_count: 79 },
		{ id: 12, group_id: 1, title: 'Lobsters', url: 'https://lobste.rs/rss', display_mode: 'headline', sort_direction: 'desc', unread_count: 40 },
		{ id: 21, group_id: 2, title: 'Board Game Quest', url: 'https://www.boardgamequest.com/feed/', display_mode: 'headline', sort_direction: 'desc', unread_count: 1 },
		{
			id: 501, group_id: 50, title: 'Scroll test feed', url: 'https://example.com/feed.xml', display_mode: 'headline', sort_direction: 'desc', unread_count: 0,
			last_refresh_error: 'http error: 404 Not Found', last_refresh_at: '2026-08-19T14:48:09Z', last_successful_refresh_at: '2026-08-18T14:48:09Z',
		},
	];
	const programmingArticles = Array.from({ length: 120 }, (_, index) => index + 1).map(id => ({
		id, feed_id: id % 3 === 0 ? 12 : 11, feed_title: id % 3 === 0 ? 'Lobsters' : 'Hacker News', title: `Example article ${id}`,
		link: `https://example.com/${id}`, comments_link: `https://example.com/${id}/comments`,
		description: `<p>Summary for article ${id}.</p><a href="https://example.com/${id}/comments">Comments</a>`,
		published_at: '2026-08-16T12:00:00Z', order_index: 10_000 - id, is_read: id === 1,
	}));
	const articles = programmingArticles.concat([
		{ id: 201, feed_id: 21, feed_title: 'Board Game Quest', title: 'First short article', link: 'https://example.com/201', description: '<p>First summary.</p>', published_at: '2026-08-16T12:00:00Z', order_index: 201, is_read: false },
		{ id: 202, feed_id: 21, feed_title: 'Board Game Quest', title: 'Second short article', link: 'https://example.com/202', description: '<p>Second summary.</p><img src="https://images.example.com/second.png" alt="Second article image">', published_at: '2026-08-16T11:00:00Z', order_index: 200, is_read: false },
	]);
	await page.route('**/api/image?url=**', route => route.fulfill({
		contentType: 'image/png',
		body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64'),
	}));
	await page.route('**/api/favicon?url=**', route => route.fulfill({
		contentType: 'image/png',
		body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64'),
	}));
	await page.route('**/api/groups', route => route.fulfill({ json: groups }));
	await page.route('**/api/feeds', route => route.fulfill({ json: feeds }));
	await page.route(/\/api\/articles\?(?:.*)$/, route => {
		const params = new URL(route.request().url()).searchParams;
		const feedID = params.get('feed_id');
		const groupID = params.get('group_id');
		const limit = Number(params.get('limit') || 30);
		const offset = Number(params.get('offset') || 0);
		const hasCursor = params.has('cursor_order_index') && params.has('cursor_id');
		const cursorOrder = Number(params.get('cursor_order_index'));
		const cursorID = Number(params.get('cursor_id'));
		const unreadOnly = params.get('unread_only') === '1';
		const groupFeedIDs = new Set(feeds.filter(feed => feed.group_id === Number(groupID)).map(feed => feed.id));
		const matching = (feedID
			? articles.filter(article => article.feed_id === Number(feedID))
			: articles.filter(article => groupFeedIDs.has(article.feed_id)))
			.filter(article => !unreadOnly || !article.is_read)
			.slice().sort((left, right) => right.order_index - left.order_index || right.id - left.id);
		const continuation = hasCursor && Number.isFinite(cursorOrder) && Number.isFinite(cursorID)
			? matching.filter(article => article.order_index < cursorOrder || (article.order_index === cursorOrder && article.id < cursorID))
			: matching.slice(offset);
		return route.fulfill({
			json: { articles: continuation.slice(0, limit), total: matching.length, has_more: continuation.length > limit },
		});
	});
	await page.route('**/api/feeds/update', route => route.fulfill({ json: { status: 'ok' } }));
	await page.route('**/api/users', route => {
		if (route.request().method() === 'GET') return route.fulfill({ json: users });
		const params = new URLSearchParams(route.request().postData() || '');
		const user = { id: users.length + 1, username: params.get('username'), is_admin: false, must_change_password: true };
		users.push(user);
		return route.fulfill({ json: user });
	});
	await page.route('**/api/feeds/delete', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const index = feeds.findIndex(feed => feed.id === Number(params.get('feed_id')));
		if (index >= 0) feeds.splice(index, 1);
		return route.fulfill({ json: { status: 'ok' } });
	});
	await page.route('**/api/releases/check', route => route.fulfill({
		json: {
			enabled: true,
			update_available: false,
			current_version: 'dev',
			releases_url: 'https://github.com/goosepod/feedss/releases',
		},
	}));
	await page.route('**/api/articles/read', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const articleID = Number(params.get('article_id'));
		const feedID = Number(params.get('feed_id'));
		const groupID = Number(params.get('group_id'));
		const readThroughOrder = Number(params.get('read_through_order_index'));
		const readThroughID = Number(params.get('read_through_id'));
		const hasReadThrough = params.has('read_through_order_index') && params.has('read_through_id');
		let updated = 0;
		for (const article of articles) {
			const feed = feeds.find(item => item.id === article.feed_id);
			const inScope = article.id === articleID || article.feed_id === feedID || feed?.group_id === groupID;
			const atOrBeforeBoundary = !hasReadThrough || article.order_index < readThroughOrder
				|| (article.order_index === readThroughOrder && article.id <= readThroughID);
			if (inScope && atOrBeforeBoundary && (!hasReadThrough || article.id <= readThroughID)) {
				if (!article.is_read) updated += 1;
				article.is_read = true;
			}
		}
		return route.fulfill({ json: { status: 'ok', updated } });
	});
	await page.route('**/api/refresh', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const feedID = Number(params.get('feed_id'));
		if (feedID) {
			const feed = feeds.find(item => item.id === feedID);
			if (feed) feed.last_refresh_error = '';
			return route.fulfill({ json: { refreshed: feed ? 1 : 0, failed: 0 } });
		}
		return route.fulfill({ json: { refreshed: 2, failed: 0 } });
	});
  await login(page);

  await expect(page.getByRole('link', { name: 'feedss', exact: true })).toBeVisible();
	await expect(page.locator('.brand-icon')).toHaveAttribute('src', '/static/favicon.svg');
	await expect(page).toHaveTitle('feedss');
	await expect.poll(() => page.locator('link[rel="icon"]').getAttribute('type')).toBe('image/png');
	await expect(page.locator('link[rel="icon"]')).toHaveAttribute('sizes', '64x64');
	expect(await page.evaluate(() => formatFaviconUnreadCount(483))).toBe('483');
	expect(await page.evaluate(() => formatFaviconUnreadCount(999))).toBe('999');
	expect(await page.evaluate(() => formatFaviconUnreadCount(1_000))).toBe('1k');
	expect(await page.evaluate(() => formatFaviconUnreadCount(2_349))).toBe('2.3k');
	expect(await page.evaluate(() => formatFaviconUnreadCount(12_832))).toBe('12k');
  await expect(page.getByRole('button', { name: 'Add feed', exact: true })).toBeVisible();
	await page.keyboard.press('?');
	const shortcutsDialog = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
	await expect(shortcutsDialog).toBeVisible();
	await expect(shortcutsDialog).toContainText('Mark all read');
	await expect(shortcutsDialog).toContainText('Refresh current view and hide read articles');
	await shortcutsDialog.getByRole('button', { name: 'Close', exact: true }).click();
	await expect(shortcutsDialog).toBeHidden();
	const subscriptions = page.locator('#subscription-list');
	await expect(subscriptions).not.toBeEmpty();
	const programmingGroup = subscriptions.locator('.subscription-group').filter({ hasText: 'Programming' });
	const groupTitle = programmingGroup.locator('.group-item');
	const groupToggle = programmingGroup.locator('.group-toggle');
	await expect(groupToggle).toHaveAttribute('aria-expanded', 'false');
	await expect(programmingGroup.locator('.feed-item')).toHaveCount(0);
	await groupTitle.click();
	await expect(page.locator('#feed-header')).toHaveText('Programming');
	await expect(programmingGroup.locator('.feed-item')).toHaveCount(0);
	await groupToggle.click();
	await expect(programmingGroup.locator('.feed-item')).toHaveCount(2);
	await expect(programmingGroup).toContainText('Hacker News');
	await expect(programmingGroup).toContainText('Lobsters');
	const sidebar = page.locator('.sidebar');
	await sidebar.evaluate(element => { element.scrollTop = 500; });
	const sidebarScrollBefore = await sidebar.evaluate(element => element.scrollTop);
	const scrollTestGroup = subscriptions.locator('.subscription-group').filter({ hasText: 'Scroll test' });
	await scrollTestGroup.locator('.group-toggle').click();
	const sidebarScrollAfter = await sidebar.evaluate(element => element.scrollTop);
	expect(Math.abs(sidebarScrollAfter - sidebarScrollBefore)).toBeLessThan(1);
	await sidebar.evaluate(element => { element.scrollTop = 0; });
	const problemFeedsButton = page.getByRole('button', { name: '1 problem feed', exact: true });
	await expect(problemFeedsButton).toBeVisible();
	await problemFeedsButton.click();
	const problemFeedsDialog = page.getByRole('dialog', { name: 'Problem feeds' });
	await expect(problemFeedsDialog).toContainText('Scroll test feed');
	await expect(problemFeedsDialog).toContainText('404 Not Found');
	await problemFeedsDialog.getByRole('button', { name: 'Edit', exact: true }).click();
	const feedSettingsDialogFromProblem = page.getByRole('dialog', { name: 'Feed settings' });
	await expect(feedSettingsDialogFromProblem.getByLabel('Feed URL')).toHaveValue('https://example.com/feed.xml');
	await feedSettingsDialogFromProblem.getByRole('button', { name: 'Cancel', exact: true }).click();
	await problemFeedsButton.click();
	await problemFeedsDialog.getByRole('button', { name: 'Retry', exact: true }).click();
	await expect(problemFeedsDialog).toContainText('No feeds currently have update problems.');
	await expect(problemFeedsButton).toBeHidden();
	await problemFeedsDialog.getByRole('button', { name: 'Close', exact: true }).click();

  const layout = await page.evaluate(() => ({
    bodyWidth: document.body.scrollWidth,
    viewportWidth: window.innerWidth,
    bodyHeight: document.body.scrollHeight,
    viewportHeight: window.innerHeight,
    sidebarClientHeight: document.querySelector('.sidebar').clientHeight,
    sidebarScrollHeight: document.querySelector('.sidebar').scrollHeight,
  }));
  expect(layout.bodyWidth).toBeLessThanOrEqual(layout.viewportWidth);
  expect(layout.bodyHeight).toBeLessThanOrEqual(layout.viewportHeight);
  expect(layout.sidebarClientHeight).toBeGreaterThan(0);

	const articleRows = page.locator('.article');
	await expect(articleRows).toHaveCount(30);
	await expect(page.locator('[data-article-id="1"]')).toHaveCount(0);
	expect(await page.evaluate(() => [
		subscriptionMetadataPollInterval(false),
		subscriptionMetadataPollInterval(true),
	])).toEqual([30_000, 15 * 60_000]);
	const articleIDsBeforeMetadataRefresh = await articleRows.evaluateAll(rows => rows.map(row => row.dataset.articleId));
	groups[0].unread_count = 125;
	feeds[0].unread_count = 85;
	await page.evaluate(() => refreshSubscriptionMetadata());
	await expect(groupTitle.locator('.nav-count')).toHaveText('125');
	await expect(programmingGroup.locator('.feed-item').filter({ hasText: 'Hacker News' }).locator('.nav-count')).toHaveText('85');
	expect(await articleRows.evaluateAll(rows => rows.map(row => row.dataset.articleId))).toEqual(articleIDsBeforeMetadataRefresh);
	groups[0].unread_count = 119;
	feeds[0].unread_count = 79;
	await page.evaluate(() => refreshSubscriptionMetadata());
	await expect(articleRows.first().locator('.article-site-icon')).toHaveAttribute('src', /\/api\/favicon\?url=https%3A%2F%2Fnews\.ycombinator\.com/);
	const firstHeadline = articleRows.first().locator('.article-title');
	await expect(firstHeadline).toHaveAttribute('href', 'https://example.com/2');
	await expect(firstHeadline).toHaveAttribute('target', '_blank');
	const firstTitleBox = await firstHeadline.boundingBox();
	const firstMetaBox = await articleRows.first().locator('.article-meta').boundingBox();
	expect(firstMetaBox.y).toBeGreaterThan(firstTitleBox.y + firstTitleBox.height - 1);
	const [headlinePage] = await Promise.all([
		page.waitForEvent('popup'),
		firstHeadline.click(),
	]);
	await expect(headlinePage).toHaveURL('https://example.com/2');
	await headlinePage.close();
	await expect(page.locator('.article.selected')).toHaveCount(0);
	await expect(page.locator('#article-count')).toHaveText('119 articles');
	await page.keyboard.press('j');
	await expect(page.locator('.article.selected')).toHaveAttribute('data-article-id', '2');
	await expect(page.locator('.article.selected')).toHaveClass(/read/);
	await expectSelectedArticleToKeepTopPadding(page);
	for (let index = 0; index < 5; index += 1) await page.keyboard.press('j');
	await expect(page.locator('.article.selected')).toHaveAttribute('data-article-id', '7');
	await expectSelectedArticleToKeepTopPadding(page);
	await page.locator('[data-article-id="10"]').click({ position: { x: 8, y: 8 } });
	await expect(page.locator('.article.selected')).toHaveAttribute('data-article-id', '10');
	await expectSelectedArticleToKeepTopPadding(page);
	await page.screenshot({ path: testInfo.outputPath(`feedss-top-inset-${testInfo.project.name}.png`) });

  await page.getByRole('button', { name: 'Add feed', exact: true }).click();
  const addDialog = page.getByRole('dialog', { name: 'Add feed' });
  await expect(addDialog).toBeVisible();
  await expect(addDialog.getByLabel('Feed URL')).toBeFocused();
  await addDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(addDialog).toBeHidden();

  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const settingsDialog = page.getByRole('dialog', { name: 'Settings' });
  await expect(settingsDialog).toBeVisible();
	await expect(page.getByLabel('Refresh interval (minutes)')).not.toHaveValue('');
	await expect(page.getByLabel('Maximum articles per feed')).toHaveAttribute('step', '1');
	expect(await page.getByLabel('Maximum articles per feed').evaluate(input => input.checkValidity())).toBe(true);
	await expect(page.getByLabel('Check for new feedss releases')).toBeChecked();
	await expect(settingsDialog.locator('.user-row').first()).toContainText('admin');
	await expect(settingsDialog.locator('.user-row').first()).toContainText('Administrator');
	await settingsDialog.getByLabel('Username').fill('new-reader');
	await settingsDialog.getByLabel('Temporary password').fill('temporary-pass');
	await settingsDialog.getByRole('button', { name: 'Add user', exact: true }).click();
	await expect(settingsDialog.locator('.user-row').filter({ hasText: 'new-reader' })).toContainText('Temporary password');
	await settingsDialog.getByLabel('Release notifications').selectOption('prerelease');
	await settingsDialog.getByRole('button', { name: 'Refresh now', exact: true }).click();
	await expect(settingsDialog.getByRole('status')).toHaveText('Updated 2 feeds.');
	await settingsDialog.getByLabel('Default display mode').selectOption('full');
	await settingsDialog.getByRole('button', { name: 'Save settings', exact: true }).click();
	await expect(settingsDialog).toBeHidden();
	await expect(page.locator('#status-message')).toHaveText('Settings saved.');
	await page.getByRole('button', { name: 'Dismiss notification', exact: true }).click();
	await expect(page.locator('#status')).toBeHidden();

	await expect(articleRows).toHaveCount(30);
	await expect(page.locator('.article-content')).toHaveCount(30);
	await expect(page.locator('#article-count')).toContainText('articles');
	const firstHeader = articleRows.first().locator('.article-header');
	if (await firstHeader.getAttribute('aria-expanded') === 'true') await firstHeader.click();
	await firstHeader.click();
	await expect(firstHeader).toHaveAttribute('aria-expanded', 'true');
	await expect(articleRows.first()).toHaveClass(/read/);

	await programmingGroup.locator('.feed-item').filter({ hasText: 'Hacker News' }).click();
	await expect(articleRows).toHaveCount(30);
	const hackerUnreadCount = articles.filter(article => article.feed_id === 11 && !article.is_read).length;
	await expect(page.locator('#article-count')).toHaveText(`${hackerUnreadCount} articles`);
	await expect(page.locator('.article-content')).toHaveCount(0);
	await page.getByRole('button', { name: 'Feed settings', exact: true }).click();
	const feedSettingsDialog = page.getByRole('dialog', { name: 'Feed settings' });
	await expect(feedSettingsDialog).toBeVisible();
	await feedSettingsDialog.getByLabel('Display mode').selectOption('full');
	await feedSettingsDialog.getByRole('button', { name: 'Save feed', exact: true }).click();
	await expect(feedSettingsDialog).toBeHidden();
	await expect(page.locator('.article-content')).toHaveCount(30);
	await expect(articleRows.first()).toHaveClass(/unread/);
	await expect(articleRows.first().getByRole('link', { name: 'Comments', exact: true })).toHaveCount(1);
	const [commentsPage] = await Promise.all([
		page.waitForEvent('popup'),
		page.keyboard.press('c'),
	]);
	await expect(commentsPage).toHaveURL(/\/comments$/);
	await commentsPage.close();
	await expect(page.locator('.article.selected')).toHaveCount(1);
	await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0);
	articles.push({
		id: 999, feed_id: 11, feed_title: 'Hacker News', title: 'New arrival', link: 'https://example.com/999',
		description: '<p>New summary.</p>', published_at: '2026-08-17T12:00:00Z', order_index: 20_000, is_read: false,
	});
	const continuationRequest = page.waitForRequest(request => {
		const url = new URL(request.url());
		return url.pathname === '/api/articles' && url.searchParams.has('cursor_order_index');
	});
	await page.locator('#article-pane').evaluate(element => { element.scrollTop = element.scrollHeight; });
	await continuationRequest;
	await expect(articleRows).toHaveCount(60);
	await expect(page.locator('[data-article-id="999"]')).toHaveCount(0);
	await programmingGroup.locator('.feed-item').filter({ hasText: 'Hacker News' }).click();
	await expect(articleRows.first()).toHaveAttribute('data-article-id', '999');
	await expect(page.locator('[data-article-id="1"]')).toHaveCount(0);
	const unreadRefreshRequest = page.waitForRequest(request => {
		const url = new URL(request.url());
		return url.pathname === '/api/articles' && url.searchParams.get('unread_only') === '1' && !url.searchParams.has('cursor_order_index');
	});
	await page.keyboard.press('r');
	await unreadRefreshRequest;
	await expect(articleRows.first()).toHaveAttribute('data-article-id', '999');
	await expect(page.locator('[data-article-id="1"]')).toHaveCount(0);
	await expect(page.locator('#article-pane')).toHaveJSProperty('scrollTop', 0);
	await page.locator('#mark-all-read-btn').click();
	const markAllReadDialog = page.getByRole('dialog', { name: 'Mark all articles read?' });
	await expect(markAllReadDialog).toBeVisible();
	await expect(markAllReadDialog).toContainText("Mark unread articles currently in Hacker News as read? Newer articles won't be affected.");
	await markAllReadDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
	await expect(markAllReadDialog).toBeHidden();
	await expect(page.locator('#mark-all-read-btn')).toBeEnabled();
	const markAllRequest = page.waitForRequest(request =>
		request.url().endsWith('/api/articles/read') && request.postData()?.includes('feed_id=11'),
	);
	await page.keyboard.press('Shift+A');
	await expect(markAllReadDialog).toBeVisible();
	articles.push({
		id: 1000, feed_id: 11, feed_title: 'Hacker News', title: 'Unseen newer arrival', link: 'https://example.com/1000',
		description: '<p>Arrived after this list was loaded.</p>', published_at: '2026-08-18T12:00:00Z', order_index: 30_000, is_read: false,
	});
	await markAllReadDialog.getByRole('button', { name: 'Mark all read', exact: true }).click();
	await markAllRequest;
	await expect(articleRows.first()).toHaveAttribute('data-article-id', '1000');
	await expect(articleRows.first()).toHaveClass(/unread/);
	await expect(page.locator('#article-pane')).toHaveJSProperty('scrollTop', 0);
	await expect(page.locator('#mark-all-read-btn')).toBeEnabled();

	const boardGamesGroup = subscriptions.locator('.subscription-group').filter({ hasText: 'Board games' });
	await boardGamesGroup.locator('.group-item').click();
	await expect(articleRows).toHaveCount(2);
	await page.keyboard.press('j');
	await page.keyboard.press('j');
	await expect(page.locator('.article.selected')).toHaveAttribute('data-article-id', '202');
	await expectSelectedArticleToKeepTopPadding(page);
	const selectedImage = page.locator('.article.selected .article-body img');
	await expect(selectedImage).toHaveAttribute('loading', 'eager');
	await expect.poll(() => selectedImage.evaluate(image => image.naturalWidth)).toBeGreaterThan(0);

  await page.screenshot({
    path: testInfo.outputPath(`feedss-${testInfo.project.name}.png`),
    fullPage: false,
  });
});

test('temporary users must choose a permanent password', async ({ page }, testInfo) => {
	await login(page);
	await page.getByRole('button', { name: 'Settings', exact: true }).click();
	const settingsDialog = page.getByRole('dialog', { name: 'Settings' });
	const username = `reader-${testInfo.project.name}`;
	await settingsDialog.getByLabel('Username').fill(username);
	await settingsDialog.getByLabel('Temporary password').fill('temporary-pass');
	await settingsDialog.getByRole('button', { name: 'Add user', exact: true }).click();
	await expect(settingsDialog.locator('.user-row').filter({ hasText: username })).toContainText('Temporary password');
	await settingsDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
	await page.getByRole('link', { name: 'Log out', exact: true }).click();

	await page.getByLabel('Username').fill(username);
	await page.getByLabel('Password').fill('temporary-pass');
	await Promise.all([
		page.waitForURL('/change-password'),
		page.getByRole('button', { name: 'Sign in', exact: true }).click(),
	]);
	await expect(page.getByRole('heading', { name: 'Choose your password', exact: true })).toBeVisible();
	await page.getByLabel('New password', { exact: true }).fill('permanent-pass');
	await page.getByLabel('Confirm new password').fill('permanent-pass');
	await Promise.all([
		page.waitForURL('/'),
		page.getByRole('button', { name: 'Save password', exact: true }).click(),
	]);
	await expect(page.locator('#feed-header')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Settings', exact: true })).toHaveCount(0);
	await page.getByRole('button', { name: 'Account', exact: true }).click();
	const accountDialog = page.getByRole('dialog', { name: 'Account' });
	await expect(accountDialog.getByLabel('Username')).toHaveValue(username);
	const renamedUsername = `${username}-renamed`;
	await accountDialog.getByLabel('Username').fill(renamedUsername);
	await accountDialog.getByLabel('Current password').fill('permanent-pass');
	await accountDialog.getByLabel('New password (leave blank to keep it)', { exact: true }).fill('updated-pass');
	await accountDialog.getByLabel('Confirm new password').fill('updated-pass');
	await accountDialog.getByRole('button', { name: 'Save account', exact: true }).click();
	await expect(accountDialog).toBeHidden();
	await expect(page.locator('#status-message')).toHaveText('Account updated.');
	await page.getByRole('link', { name: 'Log out', exact: true }).click();
	await page.getByLabel('Username').fill(renamedUsername);
	await page.getByLabel('Password').fill('updated-pass');
	await Promise.all([
		page.waitForURL('/'),
		page.getByRole('button', { name: 'Sign in', exact: true }).click(),
	]);
	await expect(page.locator('#feed-header')).toBeVisible();
});
