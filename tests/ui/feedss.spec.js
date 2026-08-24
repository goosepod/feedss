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
	await expect(page.locator('link[rel="manifest"]')).toHaveAttribute('href', '/static/manifest.webmanifest');
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

async function openMobileSubscriptions(page) {
	const toggle = page.getByRole('button', { name: 'Open subscriptions', exact: true });
	if (!await toggle.isVisible()) return;
	const bodyClass = await page.locator('body').getAttribute('class');
	if (bodyClass?.includes('mobile-actions-open')) await page.keyboard.press('Escape');
	if (await toggle.getAttribute('aria-expanded') === 'false') await toggle.click();
}

async function openMobileActions(page) {
	const toggle = page.getByRole('button', { name: 'Open actions', exact: true });
	if (!await toggle.isVisible()) return;
	const bodyClass = await page.locator('body').getAttribute('class');
	if (bodyClass?.includes('mobile-nav-open')) await page.keyboard.press('Escape');
	if (await toggle.getAttribute('aria-expanded') === 'false') await toggle.click();
}

async function openSearch(page) {
	const toggle = page.getByRole('button', { name: 'Search articles', exact: true });
	await openMobileSubscriptions(page);
	await toggle.click();
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
	let addedFeedURL = '';
	let syncRevision = 1;
	let syncArticleRevision = 1;
	let syncSubscriptionRevision = 1;
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
	await page.route('**/api/sync**', route => {
		const params = new URL(route.request().url()).searchParams;
		const sinceText = params.get('since');
		const since = Number(sinceText || 0);
		const articlesChanged = sinceText !== null && syncArticleRevision > since;
		const subscriptionsChanged = sinceText !== null && (syncSubscriptionRevision > since || articlesChanged);
		const requestedIDs = new Set((params.get('article_ids') || '').split(',').filter(Boolean).map(Number));
		return route.fulfill({ json: {
			revision: syncRevision,
			articles_changed: articlesChanged,
			subscriptions_changed: subscriptionsChanged,
			saved_count: articles.filter(article => article.is_saved).length,
			recently_read_count: articles.filter(article => article.is_read).length,
			articles: articlesChanged ? articles.filter(article => requestedIDs.has(article.id)).map(article => ({
				id: article.id, is_read: Boolean(article.is_read), is_saved: Boolean(article.is_saved),
			})) : [],
		} });
	});
	await page.route(/\/api\/articles\?(?:.*)$/, route => {
		const params = new URL(route.request().url()).searchParams;
		const view = params.get('view') || (params.get('saved') === '1' ? 'saved' : '');
		const feedID = params.get('feed_id');
		const groupID = params.get('group_id');
		const limit = Number(params.get('limit') || 30);
		const offset = Number(params.get('offset') || 0);
		const hasCursor = params.has('cursor_order_index') && params.has('cursor_id');
		const cursorOrder = Number(params.get('cursor_order_index'));
		const cursorID = Number(params.get('cursor_id'));
		const unreadOnly = params.get('unread_only') === '1';
		const groupFeedIDs = new Set(feeds.filter(feed => feed.group_id === Number(groupID)).map(feed => feed.id));
		const matching = (view === 'saved'
			? articles.filter(article => article.is_saved)
			: view === 'unread'
			? articles.filter(article => !article.is_read)
			: view === 'recent'
			? articles.filter(article => article.is_read)
			: feedID
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
	await page.route('**/api/feeds/discover', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const source = params.get('url');
		return route.fulfill({ json: { feeds: source?.includes('website.example') ? [
			{ title: 'News', url: 'https://website.example/news.xml' },
			{ title: 'Updates', url: 'https://website.example/updates.xml' },
		] : [{ title: 'Example feed', url: source }] } });
	});
	await page.route('**/feed/add', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		addedFeedURL = params.get('url') || '';
		return route.fulfill({ json: { status: 'ok' } });
	});
	await page.route('**/api/backup', route => route.fulfill({
		contentType: 'application/vnd.sqlite3',
		headers: { 'Content-Disposition': 'attachment; filename="feedss-backup-test.db"' },
		body: Buffer.from('SQLite format 3\0test'),
	}));
	await page.route(/\/api\/search\?(?:.*)$/, route => {
		const params = new URL(route.request().url()).searchParams;
		const terms = (params.get('q') || '').toLowerCase().split(/\s+/).filter(Boolean);
		const feedID = Number(params.get('feed_id'));
		const groupID = Number(params.get('group_id'));
		const limit = Number(params.get('limit') || 30);
		const offset = Number(params.get('offset') || 0);
		const matching = articles.filter(article => {
			const feed = feeds.find(item => item.id === article.feed_id);
			if (feedID && article.feed_id !== feedID) return false;
			if (groupID && feed?.group_id !== groupID) return false;
			const haystack = [article.title, article.description, article.content].filter(Boolean).join(' ').toLowerCase();
			return terms.every(term => haystack.includes(term));
		});
		return route.fulfill({
			json: { articles: matching.slice(offset, offset + limit), total: matching.length, has_more: offset + limit < matching.length },
		});
	});
	await page.route('**/api/feeds/update', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const feed = feeds.find(item => item.id === Number(params.get('feed_id')));
		if (!feed) return route.fulfill({ status: 404, body: 'feed not found' });
		const previousGroup = groups.find(group => group.id === feed.group_id);
		const nextGroupID = Number(params.get('group_id') || feed.group_id);
		const nextGroup = groups.find(group => group.id === nextGroupID);
		if (!nextGroup) return route.fulfill({ status: 400, body: 'group not found' });
		if (previousGroup?.id !== nextGroup.id) {
			previousGroup.feed_count -= 1;
			nextGroup.feed_count += 1;
		}
		feed.title = params.get('title') || feed.title;
		feed.group_id = nextGroupID;
		feed.url = params.get('url') || feed.url;
		feed.display_mode = params.get('display_mode') || feed.display_mode;
		feed.sort_direction = params.get('sort_direction') || feed.sort_direction;
		return route.fulfill({ json: { status: 'ok' } });
	});
	await page.route('**/api/groups/update', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const group = groups.find(item => item.id === Number(params.get('group_id')));
		if (!group) return route.fulfill({ status: 404, body: 'group not found' });
		group.name = params.get('name') || group.name;
		return route.fulfill({ json: { status: 'ok' } });
	});
	await page.route('**/api/groups/delete', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const index = groups.findIndex(group => group.id === Number(params.get('group_id')));
		if (index < 0) return route.fulfill({ status: 404, body: 'group not found' });
		if (groups[index].feed_count) return route.fulfill({ status: 409, body: 'move or remove the feeds in this group first' });
		groups.splice(index, 1);
		return route.fulfill({ json: { status: 'ok' } });
	});
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
		if (index >= 0) {
			const group = groups.find(group => group.id === feeds[index].group_id);
			if (group) group.feed_count -= 1;
			feeds.splice(index, 1);
		}
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
	await page.route('**/api/articles/saved', route => {
		const params = new URLSearchParams(route.request().postData() || '');
		const article = articles.find(item => item.id === Number(params.get('article_id')));
		if (!article) return route.fulfill({ status: 404, body: 'article not found' });
		article.is_saved = params.get('saved') === 'true';
		return route.fulfill({ json: { status: 'ok', saved: article.is_saved } });
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
	const searchScope = page.getByRole('group', { name: 'Search in' });
	await expect(searchScope).toBeHidden();
	await openSearch(page);
	await expect(searchScope).toBeVisible();
	await expect(searchScope.getByRole('radio', { name: 'All articles' })).toBeChecked();
	await expect(searchScope.getByRole('radio', { name: 'Current view' })).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(searchScope).toBeHidden();
	await expect(page.locator('.brand-icon')).toHaveAttribute('src', '/static/favicon.svg');
	await expect(page).toHaveTitle('feedss');
	await expect.poll(() => page.locator('link[rel="icon"]').getAttribute('type')).toBe('image/png');
	await expect(page.locator('link[rel="icon"]')).toHaveAttribute('sizes', '64x64');
	expect(await page.evaluate(() => formatFaviconUnreadCount(483))).toBe('483');
	expect(await page.evaluate(() => formatFaviconUnreadCount(999))).toBe('999');
	expect(await page.evaluate(() => formatFaviconUnreadCount(1_000))).toBe('1k');
	expect(await page.evaluate(() => formatFaviconUnreadCount(2_349))).toBe('2.3k');
	expect(await page.evaluate(() => formatFaviconUnreadCount(12_832))).toBe('12k');
	await openMobileActions(page);
	await expect(page.getByRole('button', { name: 'Add feed', exact: true })).toBeVisible();
	if (await page.getByRole('button', { name: 'Open actions', exact: true }).isVisible()) await page.keyboard.press('Escape');
	await page.keyboard.press('?');
	const shortcutsDialog = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
	await expect(shortcutsDialog).toBeVisible();
	await expect(shortcutsDialog).toContainText('Mark all read');
	await expect(shortcutsDialog).toContainText('Refresh current view and hide read articles');
	await expect(shortcutsDialog).toContainText('Show search');
	await expect(shortcutsDialog).toContainText('Add feed');
	await shortcutsDialog.getByRole('button', { name: 'Close', exact: true }).click();
	await expect(shortcutsDialog).toBeHidden();
	await page.keyboard.press('/');
	await expect(page.getByRole('searchbox', { name: 'Search articles' })).toBeFocused();
	await expect(page.locator('body')).toHaveClass(/search-open/);
	await page.keyboard.type('one/two');
	await expect(page.getByRole('searchbox', { name: 'Search articles' })).toHaveValue('one/two');
	await page.keyboard.press('Escape');
	await page.keyboard.press('/');
	await expect.poll(() => page.getByRole('searchbox', { name: 'Search articles' }).evaluate(input => ({
		start: input.selectionStart,
		end: input.selectionEnd,
		length: input.value.length,
	}))).toEqual({ start: 0, end: 7, length: 7 });
	await page.keyboard.press('Backspace');
	await expect(page.getByRole('searchbox', { name: 'Search articles' })).toHaveValue('');
	await page.keyboard.press('Escape');
	await page.keyboard.press('Shift+=');
	const shortcutAddDialog = page.getByRole('dialog', { name: 'Add feed' });
	await expect(shortcutAddDialog).toBeVisible();
	await expect(shortcutAddDialog.getByLabel('Feed URL')).toBeFocused();
	await page.keyboard.press('Escape');
	await expect(shortcutAddDialog).toBeHidden();
	const subscriptions = page.locator('#subscription-list');
	await expect(subscriptions).not.toBeEmpty();
	await openMobileSubscriptions(page);
	const programmingGroup = subscriptions.locator('.subscription-group').filter({ hasText: 'Programming' });
	const groupTitle = programmingGroup.locator('.group-item');
	const groupToggle = programmingGroup.locator('.group-toggle');
	await expect(groupToggle).toHaveAttribute('aria-expanded', 'false');
	await expect(programmingGroup.locator('.feed-item')).toHaveCount(0);
	await groupTitle.click();
	await expect(page.locator('#feed-header')).toHaveText('Programming');
	await openMobileSubscriptions(page);
	await page.getByRole('button', { name: 'All unread', exact: true }).click();
	await expect(page.locator('#feed-header')).toHaveText('All unread');
	await expect(page.locator('#article-count')).toHaveText('121 articles');
	await openMobileSubscriptions(page);
	await groupTitle.click();
	await expect(page.locator('#feed-header')).toHaveText('Programming');
	await expect(programmingGroup.locator('.feed-item')).toHaveCount(0);
	await openMobileSubscriptions(page);
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
	await openMobileActions(page);
	await expect(problemFeedsButton).toBeVisible();
	await problemFeedsButton.click();
	const problemFeedsDialog = page.getByRole('dialog', { name: 'Problem feeds' });
	await expect(problemFeedsDialog).toContainText('Scroll test feed');
	await expect(problemFeedsDialog).toContainText('404 Not Found');
	await problemFeedsDialog.getByRole('button', { name: 'Edit', exact: true }).click();
	const feedSettingsDialogFromProblem = page.getByRole('dialog', { name: 'Feed settings' });
	await expect(feedSettingsDialogFromProblem.getByLabel('Feed URL')).toHaveValue('https://example.com/feed.xml');
	await feedSettingsDialogFromProblem.getByRole('button', { name: 'Cancel', exact: true }).click();
	await openMobileActions(page);
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
		syncPollInterval(false),
		syncPollInterval(true),
	])).toEqual([3_000, 15 * 60_000]);
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
	const synchronizedArticle = articles.find(article => article.id === 29);
	synchronizedArticle.is_read = true;
	synchronizedArticle.is_saved = true;
	groups[0].unread_count -= 1;
	feeds.find(feed => feed.id === synchronizedArticle.feed_id).unread_count -= 1;
	syncRevision += 1;
	syncArticleRevision = syncRevision;
	const articleIDsBeforeSync = await articleRows.evaluateAll(rows => rows.map(row => row.dataset.articleId));
	await page.evaluate(() => synchronizeClient());
	await expect(page.locator('[data-article-id="29"]')).toHaveClass(/read/);
	await expect(page.locator('[data-article-id="29"] .save-article')).toHaveAttribute('aria-label', 'Remove from saved: Example article 29');
	expect(await articleRows.evaluateAll(rows => rows.map(row => row.dataset.articleId))).toEqual(articleIDsBeforeSync);
	feeds.push({ id: 99, group_id: 1, title: 'Remote feed', url: 'https://example.com/remote.xml', display_mode: 'headline', sort_direction: 'desc', unread_count: 0 });
	groups[0].feed_count += 1;
	syncRevision += 1;
	syncSubscriptionRevision = syncRevision;
	await page.evaluate(() => synchronizeClient());
	await openMobileSubscriptions(page);
	await expect(page.locator('.feed-item').filter({ hasText: 'Remote feed' })).toBeVisible();
	if (await page.getByRole('button', { name: 'Open subscriptions', exact: true }).isVisible()) await page.keyboard.press('Escape');
	synchronizedArticle.is_saved = false;
	syncRevision += 1;
	syncArticleRevision = syncRevision;
	await page.evaluate(() => synchronizeClient());
	await expect(page.locator('[data-article-id="29"] .save-article')).toHaveAttribute('aria-label', 'Save: Example article 29');
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
	await page.keyboard.press('s');
	await expect(page.locator('[data-article-id="10"] .save-article')).toHaveAttribute('aria-label', 'Remove from saved: Example article 10');
	await openMobileSubscriptions(page);
	await page.getByRole('button', { name: 'Saved articles', exact: true }).click();
	await expect(page.locator('#feed-header')).toHaveText('Saved articles');
	await expect(articleRows).toHaveCount(1);
	await expect(articleRows.first()).toHaveAttribute('data-article-id', '10');
	await expect(page.locator('#mark-all-read-btn')).toBeDisabled();
	await openSearch(page);
	await page.getByRole('searchbox', { name: 'Search articles' }).fill('Example article 12');
	await page.getByRole('button', { name: 'Search', exact: true }).click();
	await expect(page.locator('#feed-header')).toHaveText('Results for “Example article 12”');
	await expect(page.locator('[data-article-id="12"]')).toHaveCount(1);
	await openMobileSubscriptions(page);
	await groupTitle.click();
	await expect(page.locator('#feed-header')).toHaveText('Programming');
	await openMobileSubscriptions(page);
	await page.getByRole('button', { name: 'Recently read', exact: true }).click();
	await expect(page.locator('#feed-header')).toHaveText('Recently read');
	await expect(page.locator('[data-article-id="10"]')).toHaveCount(1);
	await openMobileSubscriptions(page);
	await groupTitle.click();
	await expect(page.locator('#feed-header')).toHaveText('Programming');

	await openMobileActions(page);
  await page.getByRole('button', { name: 'Add feed', exact: true }).click();
  const addDialog = page.getByRole('dialog', { name: 'Add feed' });
  await expect(addDialog).toBeVisible();
  await expect(addDialog.getByLabel('Feed URL')).toBeFocused();
	await addDialog.getByLabel('Feed URL').fill('https://website.example');
	await addDialog.getByRole('button', { name: 'Add feed', exact: true }).click();
	await expect(addDialog.getByRole('radiogroup', { name: 'Discovered feeds' })).toBeVisible();
	await addDialog.getByRole('radio', { name: /Updates/ }).check();
	await addDialog.getByRole('button', { name: 'Add selected feed', exact: true }).click();
	await expect(addDialog).toBeHidden();
	expect(addedFeedURL).toBe('https://website.example/updates.xml');

	await openMobileActions(page);
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
	const backupDownload = page.waitForEvent('download');
	await settingsDialog.getByRole('button', { name: 'Download backup', exact: true }).click();
	expect((await backupDownload).suggestedFilename()).toBe('feedss-backup-test.db');
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

	await openMobileSubscriptions(page);
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
	await openMobileSubscriptions(page);
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
	await openMobileSubscriptions(page);
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

	await openMobileSubscriptions(page);
	const boardGamesToggle = boardGamesGroup.locator('.group-toggle');
	if (await boardGamesToggle.getAttribute('aria-expanded') === 'false') await boardGamesToggle.click();
	await boardGamesGroup.locator('.feed-item').filter({ hasText: 'Board Game Quest' }).click();
	await page.getByRole('button', { name: 'Feed settings', exact: true }).click();
	const removalSettingsDialog = page.getByRole('dialog', { name: 'Feed settings' });
	await expect(removalSettingsDialog.getByLabel('Feed name')).toHaveValue('Board Game Quest');
	await expect(removalSettingsDialog.getByLabel('Group', { exact: true })).toHaveValue('2');
	await removalSettingsDialog.getByLabel('Feed name').fill('Tabletop News');
	await removalSettingsDialog.getByLabel('Group', { exact: true }).selectOption({ label: 'Programming' });
	await removalSettingsDialog.getByRole('button', { name: 'Save feed', exact: true }).click();
	await expect(page.locator('#feed-header')).toHaveText('Tabletop News');
	await openMobileSubscriptions(page);
	await expect(boardGamesGroup.locator('.feed-item')).toHaveCount(0);
	await programmingGroup.locator('.feed-item').filter({ hasText: 'Tabletop News' }).click();
	await page.getByRole('button', { name: 'Feed settings', exact: true }).click();
	await removalSettingsDialog.getByRole('button', { name: 'Remove feed', exact: true }).click();
	const removeFeedDialog = page.getByRole('dialog', { name: 'Remove feed?' });
	await expect(removeFeedDialog).toContainText('Remove Tabletop News?');
	await removeFeedDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
	await expect(removalSettingsDialog).toBeVisible();
	await removalSettingsDialog.getByRole('button', { name: 'Remove feed', exact: true }).click();
	await removeFeedDialog.getByRole('button', { name: 'Remove feed', exact: true }).click();
	await expect(removeFeedDialog).toBeHidden();
	await expect(programmingGroup.locator('.feed-item').filter({ hasText: 'Tabletop News' })).toHaveCount(0);
	await expect(page.locator('#status-message')).toHaveText('Tabletop News removed.');

	await openMobileSubscriptions(page);
	await boardGamesGroup.locator('.group-item').click();
	await page.getByRole('button', { name: 'Group settings', exact: true }).click();
	const groupSettingsDialog = page.getByRole('dialog', { name: 'Group settings' });
	await expect(groupSettingsDialog).toContainText('0 feeds in this group.');
	await groupSettingsDialog.getByLabel('Group name').fill('Games archive');
	await groupSettingsDialog.getByRole('button', { name: 'Save group', exact: true }).click();
	await expect(page.locator('#feed-header')).toHaveText('Games archive');
	await page.getByRole('button', { name: 'Group settings', exact: true }).click();
	await groupSettingsDialog.getByRole('button', { name: 'Remove group', exact: true }).click();
	const removeGroupDialog = page.getByRole('dialog', { name: 'Remove group?' });
	await expect(removeGroupDialog).toContainText('Remove Games archive?');
	await removeGroupDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
	await expect(groupSettingsDialog).toBeVisible();
	await groupSettingsDialog.getByRole('button', { name: 'Remove group', exact: true }).click();
	await removeGroupDialog.getByRole('button', { name: 'Remove group', exact: true }).click();
	await expect(removeGroupDialog).toBeHidden();
	await openMobileSubscriptions(page);
	await expect(subscriptions.locator('.subscription-group').filter({ hasText: 'Games archive' })).toHaveCount(0);
	await expect(page.locator('#status-message')).toHaveText('Games archive removed.');
});

test('installed app provides a private-data-safe offline shell', async ({ page, context }) => {
	await login(page);
	await page.evaluate(() => navigator.serviceWorker.ready.then(registration => Boolean(registration.active)));
	await page.waitForFunction(() => Boolean(navigator.serviceWorker.controller));
	await context.setOffline(true);
	try {
		await page.reload({ waitUntil: 'domcontentloaded' });
		await expect(page.getByRole('heading', { name: 'feedss is offline' })).toBeVisible();
		await expect(page.getByText('Reconnect to the server')).toBeVisible();
	} finally {
		await context.setOffline(false);
	}
});

test('invalid credentials stay on the login page with an error', async ({ page }) => {
	await page.goto('/login');
	await page.getByLabel('Username').fill('admin');
	await page.getByLabel('Password').fill('wrong-password');
	await page.getByRole('button', { name: 'Sign in', exact: true }).click();

	await expect(page).toHaveURL(/\/login$/);
	await expect(page.getByRole('alert')).toHaveText('The username or password is incorrect.');
	await expect(page.getByLabel('Username')).toHaveValue('admin');
	await expect(page.getByLabel('Password')).toHaveValue('');
	await expect(page.getByLabel('Password')).toBeFocused();
});

test('temporary users must choose a permanent password', async ({ page }, testInfo) => {
	await login(page);
	await openMobileActions(page);
	await page.getByRole('button', { name: 'Settings', exact: true }).click();
	const settingsDialog = page.getByRole('dialog', { name: 'Settings' });
	const username = `reader-${testInfo.project.name}`;
	await settingsDialog.getByLabel('Username').fill(username);
	await settingsDialog.getByLabel('Temporary password').fill('temporary-pass');
	await settingsDialog.getByRole('button', { name: 'Add user', exact: true }).click();
	await expect(settingsDialog.locator('.user-row').filter({ hasText: username })).toContainText('Temporary password');
	await settingsDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
	await openMobileActions(page);
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
	await openMobileActions(page);
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
	await openMobileActions(page);
	await page.getByRole('link', { name: 'Log out', exact: true }).click();
	await page.getByLabel('Username').fill(renamedUsername);
	await page.getByLabel('Password').fill('updated-pass');
	await Promise.all([
		page.waitForURL('/'),
		page.getByRole('button', { name: 'Sign in', exact: true }).click(),
	]);
	await expect(page.locator('#feed-header')).toBeVisible();
});
