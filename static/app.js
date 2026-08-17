const ARTICLE_PAGE_SIZE = 30;
const DEFAULT_TITLE = 'feedss';
const STATIC_FAVICON = '/static/favicon.svg';

const state = {
  groups: [],
  feeds: [],
  articles: [],
	articleTotal: 0,
	articleOffset: 0,
	articlesLoading: false,
  selectedGroupId: null,
  selectedFeedId: null,
  viewMode: 'group',
	defaultDisplayMode: 'headline',
	defaultSortOrder: 'desc',
	releaseCheckEnabled: true,
	releaseCheckIncludePrereleases: false,
	selectedArticleIndex: -1,
  articleRequest: 0,
  expandedGroupIds: new Set(),
  expandedArticleIds: new Set(),
};

const elements = {};

function setStatus(message = '', type = 'info') {
  elements.statusMessage.textContent = message;
  elements.status.className = `status${type === 'error' ? ' error' : ''}`;
  elements.status.hidden = !message;
}

function setFormError(element, message = '') {
  element.textContent = message;
  element.hidden = !message;
}

async function request(url, options = {}) {
  const response = await fetch(url, { credentials: 'same-origin', ...options });
  if (response.redirected && new URL(response.url).pathname === '/login') {
    window.location.assign('/login');
    throw new Error('Your session has expired.');
  }
  if (!response.ok) {
    const detail = (await response.text()).trim();
    throw new Error(detail || `Request failed (${response.status})`);
  }
  return response;
}

async function fetchJson(url, options) {
  return (await request(url, options)).json();
}

async function loadSettings() {
  const settings = await fetchJson('/api/settings');
	applySettings(settings);
}

function applySettings(settings) {
	state.defaultDisplayMode = settings.default_display_mode || 'headline';
	state.defaultSortOrder = settings.default_sort_order || 'desc';
  elements.settingsRefresh.value = settings.refresh_interval_min ?? 15;
  elements.settingsMax.value = settings.max_articles_per_feed ?? 500;
  elements.settingsDisplay.value = settings.default_display_mode || 'headline';
  elements.settingsSort.value = settings.default_sort_order || 'desc';
  elements.settingsAuto.checked = Boolean(settings.auto_refresh_enabled);
	state.releaseCheckEnabled = Boolean(settings.release_check_enabled);
	state.releaseCheckIncludePrereleases = Boolean(settings.release_check_include_prereleases);
	elements.settingsReleaseCheck.checked = state.releaseCheckEnabled;
	elements.settingsReleaseChannel.value = state.releaseCheckIncludePrereleases ? 'prerelease' : 'stable';
}

async function loadGroups() {
  const data = await fetchJson('/api/groups');
  state.groups = Array.isArray(data) ? data : [];
  if (!state.groups.some(group => group.id === state.selectedGroupId)) {
    state.selectedGroupId = state.groups[0]?.id ?? null;
  }
	state.expandedGroupIds = new Set(
		[...state.expandedGroupIds].filter(id => state.groups.some(group => group.id === id)),
	);
	renderSubscriptions();
	updateBrowserUnreadBadge();
}

async function loadFeeds() {
  const data = await fetchJson('/api/feeds');
  state.feeds = Array.isArray(data) ? data : [];
  const visibleFeeds = getVisibleFeeds();
	if (state.viewMode === 'feed' && !visibleFeeds.some(feed => feed.id === state.selectedFeedId)) {
    state.selectedFeedId = visibleFeeds[0]?.id ?? null;
  }
	renderSubscriptions();
	if (state.viewMode === 'group' && state.selectedGroupId !== null) {
		await loadGroupArticles(state.selectedGroupId);
	} else if (state.selectedFeedId) await loadArticles(state.selectedFeedId);
  else {
    state.articles = [];
	state.articleTotal = 0;
	state.articleOffset = 0;
    renderArticles();
  }
}

async function loadGroupArticles(groupID) {
	const requestID = ++state.articleRequest;
	const group = state.groups.find(item => item.id === groupID);
	elements.readerLabel.textContent = 'Current group';
	elements.feedHeader.textContent = group?.name || 'Loading group';
	state.articleTotal = 0;
	state.articleOffset = 0;
	elements.articlePane.innerHTML = '<div class="empty-state">Loading articles...</div>';
	updateArticleControls();
	try {
		const data = await fetchJson(`/api/articles?group_id=${encodeURIComponent(groupID)}&limit=${ARTICLE_PAGE_SIZE}&offset=0`);
		if (requestID !== state.articleRequest) return;
		state.articles = Array.isArray(data?.articles) ? data.articles : [];
		state.articleOffset = state.articles.length;
		state.articleTotal = Number.isFinite(data?.total) ? data.total : state.articles.length;
		state.selectedArticleIndex = -1;
		state.expandedArticleIds = new Set(
			state.defaultDisplayMode === 'headline' ? [] : state.articles.map(article => article.id),
		);
		elements.articlePane.scrollTop = 0;
		renderArticles();
	} catch (error) {
		if (requestID !== state.articleRequest) return;
		state.articles = [];
		state.articleTotal = 0;
		state.articleOffset = 0;
		renderArticles();
		setStatus(`Could not load group articles: ${error.message}`, 'error');
	}
}

async function loadArticles(feedID) {
  const requestID = ++state.articleRequest;
  const feed = state.feeds.find(item => item.id === feedID);
	elements.readerLabel.textContent = 'Current feed';
  elements.feedHeader.textContent = feed?.title || 'Loading feed';
	state.articleTotal = 0;
	state.articleOffset = 0;
  elements.articlePane.innerHTML = '<div class="empty-state">Loading articles...</div>';
  updateArticleControls();
  try {
    const data = await fetchJson(`/api/articles?feed_id=${encodeURIComponent(feedID)}&limit=${ARTICLE_PAGE_SIZE}&offset=0`);
    if (requestID !== state.articleRequest) return;
	state.articles = prioritizeUnread(Array.isArray(data?.articles) ? data.articles : []);
	state.articleOffset = state.articles.length;
	state.articleTotal = Number.isFinite(data?.total) ? data.total : state.articles.length;
		state.selectedArticleIndex = -1;
	state.expandedArticleIds = new Set(
	  feed?.display_mode === 'headline' ? [] : state.articles.map(article => article.id),
	);
	elements.articlePane.scrollTop = 0;
    renderArticles();
  } catch (error) {
    if (requestID !== state.articleRequest) return;
    state.articles = [];
	state.articleTotal = 0;
	state.articleOffset = 0;
    renderArticles();
    setStatus(`Could not load articles: ${error.message}`, 'error');
  }
}

function getVisibleFeeds() {
  return state.selectedGroupId === null
    ? state.feeds
    : state.feeds.filter(feed => feed.group_id === state.selectedGroupId);
}

function createNavButton(className, label, active, onClick, count) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = `${className}${active ? ' active' : ''}`;
  if (active) button.setAttribute('aria-current', 'true');
  const text = document.createElement('span');
  text.textContent = label;
  button.appendChild(text);
  if (count !== undefined) {
    const badge = document.createElement('span');
    badge.className = 'nav-count';
    badge.textContent = String(count);
    button.appendChild(badge);
  }
  button.addEventListener('click', onClick);
  return button;
}

function renderSubscriptions() {
	const sidebar = elements.subscriptionList.closest('.sidebar');
	const scrollTop = sidebar?.scrollTop || 0;
  elements.subscriptionList.replaceChildren();
  if (!state.groups.length) {
    elements.subscriptionList.innerHTML = '<p class="nav-empty">No subscriptions yet.</p>';
		if (sidebar) sidebar.scrollTop = scrollTop;
    return;
  }
  for (const group of state.groups) {
    const groupElement = document.createElement('div');
    groupElement.className = 'subscription-group';
    const expanded = state.expandedGroupIds.has(group.id);
		const groupRow = document.createElement('div');
		groupRow.className = `group-row${state.viewMode === 'group' && group.id === state.selectedGroupId ? ' active' : ''}`;
		const toggleButton = document.createElement('button');
		toggleButton.type = 'button';
		toggleButton.className = 'group-toggle';
		toggleButton.setAttribute('aria-expanded', String(expanded));
		toggleButton.setAttribute('aria-label', `${expanded ? 'Collapse' : 'Expand'} ${group.name}`);
		toggleButton.addEventListener('click', () => toggleGroup(group.id));
    const groupButton = createNavButton(
			'group-item', group.name, state.viewMode === 'group' && group.id === state.selectedGroupId,
			() => selectGroup(group.id), group.unread_count || 0,
    );
		groupRow.append(toggleButton, groupButton);
		groupElement.appendChild(groupRow);

    if (expanded) {
      const feedList = document.createElement('div');
      feedList.className = 'group-feeds';
      const feeds = state.feeds.filter(feed => feed.group_id === group.id);
      if (!feeds.length) {
        feedList.innerHTML = '<p class="nav-empty">No feeds in this group.</p>';
      }
      for (const feed of feeds) {
        feedList.appendChild(createNavButton(
          'feed-item', feed.title || feed.url, feed.id === state.selectedFeedId,
          () => selectFeed(feed.id), feed.unread_count || 0,
        ));
      }
      groupElement.appendChild(feedList);
    }
    elements.subscriptionList.appendChild(groupElement);
  }
	if (sidebar) sidebar.scrollTop = scrollTop;
	updateBrowserUnreadBadge();
}

function totalUnreadCount() {
	return state.groups.reduce((sum, group) => sum + (Number(group.unread_count) || 0), 0);
}

function formatFaviconUnreadCount(count) {
	if (count < 1000) return String(count);
	if (count < 1_000_000) return `${Math.floor(count / 1000)}k`;
	return `${Math.floor(count / 1_000_000)}m`;
}

function roundedRectPath(context, x, y, width, height, radius) {
	context.beginPath();
	context.moveTo(x + radius, y);
	context.lineTo(x + width - radius, y);
	context.quadraticCurveTo(x + width, y, x + width, y + radius);
	context.lineTo(x + width, y + height - radius);
	context.quadraticCurveTo(x + width, y + height, x + width - radius, y + height);
	context.lineTo(x + radius, y + height);
	context.quadraticCurveTo(x, y + height, x, y + height - radius);
	context.lineTo(x, y + radius);
	context.quadraticCurveTo(x, y, x + radius, y);
	context.closePath();
}

function updateBrowserUnreadBadge() {
	const unread = totalUnreadCount();
	document.title = DEFAULT_TITLE;
	const favicon = elements.favicon || document.getElementById('favicon');
	if (!favicon) return;
	if (unread <= 0) {
		favicon.type = 'image/svg+xml';
		favicon.removeAttribute('sizes');
		favicon.href = STATIC_FAVICON;
		return;
	}
	const canvas = document.createElement('canvas');
	canvas.width = 64;
	canvas.height = 64;
	const context = canvas.getContext('2d');
	if (!context) return;
	context.clearRect(0, 0, 64, 64);
	roundedRectPath(context, 1, 1, 62, 62, 13);
	context.fillStyle = '#176b4d';
	context.fill();
	const badgeText = formatFaviconUnreadCount(unread);
	const fontSize = badgeText.length <= 1 ? 40 : (badgeText.length === 2 ? 34 : (badgeText.length === 3 ? 27 : 22));
	context.fillStyle = '#ffffff';
	context.font = `800 ${fontSize}px Arial, sans-serif`;
	context.textAlign = 'center';
	context.textBaseline = 'middle';
	context.fillText(badgeText, 32, 34);
	favicon.type = 'image/png';
	favicon.sizes = '64x64';
	favicon.href = canvas.toDataURL('image/png');
}

function selectGroup(groupID) {
  state.selectedGroupId = groupID;
	state.selectedFeedId = null;
	state.viewMode = 'group';
  state.articles = [];
	state.articleTotal = 0;
	state.articleOffset = 0;
  renderSubscriptions();
	loadGroupArticles(groupID);
}

function toggleGroup(groupID) {
  if (state.expandedGroupIds.has(groupID)) {
    state.expandedGroupIds.delete(groupID);
    renderSubscriptions();
    return;
  }
	state.expandedGroupIds.add(groupID);
	renderSubscriptions();
}

function selectFeed(feedID) {
  if (feedID === state.selectedFeedId) return;
  const feed = state.feeds.find(item => item.id === feedID);
  if (feed) {
    state.selectedGroupId = feed.group_id;
  }
  state.selectedFeedId = feedID;
	state.viewMode = 'feed';
  renderSubscriptions();
  loadArticles(feedID);
}

function safeExternalURL(rawURL) {
  if (!rawURL) return '';
  try {
    const parsed = new URL(rawURL, window.location.origin);
    return ['http:', 'https:'].includes(parsed.protocol) ? parsed.href : '';
  } catch {
    return '';
  }
}

function proxiedImageURL(rawURL) {
	const url = safeExternalURL(rawURL);
	return url ? `/api/image?url=${encodeURIComponent(url)}` : '';
}

function siteFaviconURL(rawURL) {
	const url = safeExternalURL(rawURL);
	if (!url) return '';
	try {
		const siteURL = new URL(url);
		return `/api/favicon?url=${encodeURIComponent(siteURL.origin)}`;
	} catch {
		return '';
	}
}

function createSiteIcon(article) {
	const feed = state.feeds.find(item => item.id === article.feed_id);
	const faviconURL = siteFaviconURL(feed?.url || article.link);
	if (!faviconURL) return null;
	const icon = document.createElement('img');
	icon.className = 'article-site-icon';
	icon.src = faviconURL;
	icon.alt = '';
	icon.loading = 'lazy';
	icon.addEventListener('error', () => icon.remove(), { once: true });
	return icon;
}

function sanitizeHTML(rawHTML) {
  const parsed = new DOMParser().parseFromString(rawHTML || '', 'text/html');
  parsed.querySelectorAll('script, style, iframe, object, embed, form, input, button, picture source').forEach(node => node.remove());
  parsed.body.querySelectorAll('*').forEach(node => {
    for (const attribute of [...node.attributes]) {
      const name = attribute.name.toLowerCase();
      if (name.startsWith('on') || name === 'style' || name === 'srcdoc') node.removeAttribute(attribute.name);
			if (name === 'srcset') node.removeAttribute(attribute.name);
      if (['href', 'src', 'poster'].includes(name)) {
				const safeURL = (node.tagName === 'IMG' && name === 'src') || name === 'poster'
					? proxiedImageURL(attribute.value)
					: safeExternalURL(attribute.value);
        if (safeURL) node.setAttribute(attribute.name, safeURL);
        else node.removeAttribute(attribute.name);
      }
    }
    if (node.tagName === 'A') {
      node.setAttribute('target', '_blank');
      node.setAttribute('rel', 'noopener noreferrer');
    }
    if (node.tagName === 'IMG') node.setAttribute('loading', 'lazy');
  });
  return parsed.body.innerHTML;
}

function createArticleLink(label, rawURL) {
  const url = safeExternalURL(rawURL);
  if (!url) return null;
  const link = document.createElement('a');
  link.href = url;
  link.target = '_blank';
  link.rel = 'noopener noreferrer';
  link.textContent = label;
  return link;
}

function getCurrentDisplayMode() {
	const feed = state.feeds.find(item => item.id === state.selectedFeedId);
	return state.viewMode === 'group' ? state.defaultDisplayMode : (feed?.display_mode || 'headline');
}

function prioritizeUnread(articles) {
	return [
		...articles.filter(article => !article.is_read),
		...articles.filter(article => article.is_read),
	];
}

function removeDuplicateCommentsLink(body, commentsLink) {
	const commentsURL = safeExternalURL(commentsLink);
	if (!commentsURL) return;
	body.querySelectorAll('a').forEach(link => {
		if (safeExternalURL(link.getAttribute('href')) !== commentsURL) return;
		const parent = link.parentElement;
		link.remove();
		if (parent && parent !== body && !parent.textContent.trim() && !parent.querySelector('img, video, audio')) {
			parent.remove();
		}
	});
}

function createArticleContent(article) {
	const content = document.createElement('div');
	content.className = 'article-content';
	const links = document.createElement('div');
	links.className = 'article-links';
	const articleLink = createArticleLink('Open original', article.link);
	const commentsLink = createArticleLink('Comments', article.comments_link);
	if (articleLink) links.appendChild(articleLink);
	if (commentsLink) links.appendChild(commentsLink);
	if (!article.is_read) {
		const readButton = document.createElement('button');
		readButton.type = 'button';
		readButton.className = 'text-button mark-read';
		readButton.textContent = 'Mark read';
		readButton.addEventListener('click', () => markArticleRead(article));
		links.appendChild(readButton);
	}
	if (links.childElementCount) content.appendChild(links);

	const body = document.createElement('div');
	body.className = 'article-body';
	const articleSource = getCurrentDisplayMode() === 'full'
		? (article.content || article.description) : (article.description || article.content);
	body.innerHTML = sanitizeHTML(articleSource) || '<p>No preview is available.</p>';
	removeDuplicateCommentsLink(body, article.comments_link);
	if (body.textContent.trim() || body.querySelector('img, video, audio')) content.appendChild(body);

	const mediaURL = safeExternalURL(article.media_url);
	if (mediaURL && getCurrentDisplayMode() === 'full') {
		const video = document.createElement('video');
		video.controls = true;
		video.src = mediaURL;
		content.appendChild(video);
	}
	return content;
}

function renderArticles() {
  const feed = state.feeds.find(item => item.id === state.selectedFeedId);
	const group = state.groups.find(item => item.id === state.selectedGroupId);
	const source = state.viewMode === 'group' ? group : feed;
	elements.feedHeader.textContent = source?.name || source?.title || 'Select a subscription';
  elements.articlePane.replaceChildren();
	if (!source) {
		elements.articlePane.innerHTML = '<div class="empty-state">Add or choose a subscription to start reading.</div>';
    updateArticleControls();
    return;
  }
  if (!state.articles.length) {
		elements.articlePane.innerHTML = '<div class="empty-state">No articles are available here yet.</div>';
    updateArticleControls();
    return;
  }

  state.articles.forEach((article, index) => {
    const expanded = state.expandedArticleIds.has(article.id);
    const articleElement = document.createElement('article');
    articleElement.className = `article${article.is_read ? ' read' : ' unread'}${index === state.selectedArticleIndex ? ' selected' : ''}`;
    articleElement.dataset.articleId = String(article.id);

    const header = document.createElement('div');
    header.className = 'article-header';
    header.setAttribute('aria-expanded', String(expanded));
    const articleURL = safeExternalURL(article.link);
    const title = document.createElement(articleURL ? 'a' : 'span');
    title.className = 'article-title';
    title.textContent = article.title || 'Untitled article';
		if (articleURL) {
			title.href = articleURL;
			title.target = '_blank';
			title.rel = 'noopener noreferrer';
		}
    const meta = document.createElement('span');
    meta.className = 'article-meta';
    const date = article.published_at ? new Date(article.published_at) : null;
		const dateText = date && !Number.isNaN(date.valueOf()) ? date.toLocaleString() : '';
		const metaText = document.createElement('span');
		metaText.textContent = state.viewMode === 'group'
			? [article.feed_title, dateText].filter(Boolean).join(' - ')
			: (dateText || article.feed_title || feed.title);
		if (state.viewMode === 'group' && article.feed_title) {
			const siteIcon = createSiteIcon(article);
			if (siteIcon) meta.appendChild(siteIcon);
		}
		meta.appendChild(metaText);
    header.append(title, meta);
    articleElement.appendChild(header);

    if (expanded) {
		articleElement.appendChild(createArticleContent(article));
    }
		articleElement.addEventListener('click', event => {
			if (event.target.closest('a, .text-button, video, audio')) return;
			activateArticle(article, index);
		});
    elements.articlePane.appendChild(articleElement);
  });
	if (state.articles.length < state.articleTotal) {
		const loadMore = document.createElement('button');
		loadMore.type = 'button';
		loadMore.className = 'load-more';
		loadMore.textContent = state.articlesLoading ? 'Loading...' : 'Load more';
		loadMore.disabled = state.articlesLoading;
		loadMore.addEventListener('click', loadMoreArticles);
		elements.articlePane.appendChild(loadMore);
	}
  updateArticleControls();
}

function updateArticleControls() {
	const total = state.articleTotal;
	const source = state.viewMode === 'group'
		? state.groups.find(item => item.id === state.selectedGroupId)
		: state.feeds.find(item => item.id === state.selectedFeedId);
	const unread = source?.unread_count ?? state.articles.filter(article => !article.is_read).length;
	elements.articleCount.textContent = `${total} article${total === 1 ? '' : 's'}`;
	elements.markAllRead.disabled = unread === 0;
	elements.feedSettingsButton.hidden = state.viewMode !== 'feed' || state.selectedFeedId === null;
}

async function moveArticle(offset) {
  if (!state.articles.length) return;
	if (state.selectedArticleIndex < 0 && offset < 0) return;
	if (offset > 0 && state.selectedArticleIndex === state.articles.length - 1 && state.articles.length < state.articleTotal) {
		await loadMoreArticles();
	}
	const nextIndex = state.selectedArticleIndex < 0
		? 0
		: Math.max(0, Math.min(state.selectedArticleIndex + offset, state.articles.length - 1));
  if (nextIndex === state.selectedArticleIndex) return;
	const article = state.articles[nextIndex];
	activateArticle(article, nextIndex);
}

function activateArticle(article, index) {
	const previous = elements.articlePane.querySelector('.article.selected');
	if (previous) previous.classList.remove('selected');
	state.selectedArticleIndex = index;
	state.expandedArticleIds.add(article.id);
	const target = elements.articlePane.querySelector(`[data-article-id="${article.id}"]`);
	if (!target) return;
	target.classList.add('selected');
	const header = target.querySelector('.article-header');
	header?.setAttribute('aria-expanded', 'true');
	if (!target.querySelector('.article-content')) target.appendChild(createArticleContent(article));
	target.querySelectorAll('img').forEach(image => {
		image.loading = 'eager';
	});
	target.scrollIntoView({ block: 'start' });
	markArticleRead(article);
}

async function loadMoreArticles() {
	if (state.articlesLoading || state.articles.length >= state.articleTotal) return false;
	state.articlesLoading = true;
	const requestID = state.articleRequest;
	const parameter = state.viewMode === 'group' ? 'group_id' : 'feed_id';
	const id = state.viewMode === 'group' ? state.selectedGroupId : state.selectedFeedId;
	try {
		const data = await fetchJson(`/api/articles?${parameter}=${encodeURIComponent(id)}&limit=${ARTICLE_PAGE_SIZE}&offset=${state.articleOffset}`);
		if (requestID !== state.articleRequest) return false;
		const received = Array.isArray(data?.articles) ? data.articles : [];
		state.articleOffset += received.length;
		const existingIDs = new Set(state.articles.map(article => article.id));
		const articles = received.filter(article => !existingIDs.has(article.id));
		state.articles.push(...articles);
		state.articleTotal = Number.isFinite(data?.total) ? data.total : state.articles.length;
		if (getCurrentDisplayMode() !== 'headline') {
			articles.forEach(article => state.expandedArticleIds.add(article.id));
		}
		return articles.length > 0;
	} catch (error) {
		setStatus(`Could not load more articles: ${error.message}`, 'error');
		return false;
	} finally {
		state.articlesLoading = false;
		if (requestID === state.articleRequest) renderArticles();
	}
}

function adjustUnreadCounts(feed, amount) {
	feed.unread_count = Math.max(0, (feed.unread_count || 0) + amount);
	const group = state.groups.find(item => item.id === feed.group_id);
	if (group) group.unread_count = Math.max(0, (group.unread_count || 0) + amount);
	renderSubscriptions();
}

async function markArticleRead(article) {
	if (article.is_read) return;
	const feed = state.feeds.find(item => item.id === article.feed_id);
	article.is_read = true;
	state.articleOffset = Math.max(0, state.articleOffset - 1);
	if (feed) adjustUnreadCounts(feed, -1);
	const articleElement = elements.articlePane.querySelector(`[data-article-id="${article.id}"]`);
	articleElement?.classList.remove('unread');
	articleElement?.classList.add('read');
	articleElement?.querySelector('.mark-read')?.remove();
	updateArticleControls();
	try {
		await fetchJson('/api/articles/read', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({ article_id: article.id }),
		});
	} catch (error) {
		article.is_read = false;
		state.articleOffset += 1;
		if (feed) adjustUnreadCounts(feed, 1);
		articleElement?.classList.remove('read');
		articleElement?.classList.add('unread');
		if (articleElement?.querySelector('.article-content')) renderArticles();
		setStatus(`Could not mark article read: ${error.message}`, 'error');
	}
}

async function markAllRead() {
	const mode = state.viewMode;
	const feed = state.feeds.find(item => item.id === state.selectedFeedId);
	const group = state.groups.find(item => item.id === state.selectedGroupId);
	if (mode === 'feed' && !feed) return;
	if (mode === 'group' && !group) return;
	const unread = state.articles.filter(article => !article.is_read);
	if ((mode === 'group' ? group.unread_count : feed.unread_count) === 0 && !unread.length) return;
	const previousGroupUnread = group?.unread_count || 0;
	const previousFeedUnread = new Map(state.feeds.map(item => [item.id, item.unread_count || 0]));
	unread.forEach(article => { article.is_read = true; });
	if (mode === 'group') {
		state.feeds.filter(item => item.group_id === group.id).forEach(item => { item.unread_count = 0; });
		group.unread_count = 0;
		renderSubscriptions();
	} else {
		const groupForFeed = state.groups.find(item => item.id === feed.group_id);
		if (groupForFeed) groupForFeed.unread_count = Math.max(0, groupForFeed.unread_count - feed.unread_count);
		feed.unread_count = 0;
		renderSubscriptions();
	}
	renderArticles();
	try {
		await fetchJson('/api/articles/read', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams(mode === 'group' ? { group_id: group.id } : { feed_id: feed.id }),
		});
		if (mode === 'group') await loadGroupArticles(group.id);
		else await loadArticles(feed.id);
	} catch (error) {
		unread.forEach(article => { article.is_read = false; });
		state.feeds.forEach(item => { item.unread_count = previousFeedUnread.get(item.id) || 0; });
		if (group) group.unread_count = previousGroupUnread;
		renderSubscriptions();
		renderArticles();
		setStatus(`Could not mark ${mode} read: ${error.message}`, 'error');
	}
}

async function saveFeed() {
  setFormError(elements.feedFormError);
  elements.saveFeed.disabled = true;
  const form = new URLSearchParams({
    url: elements.feedURL.value.trim(),
    group: elements.feedGroup.value.trim(),
    display_mode: elements.feedDisplay.value,
    sort_direction: elements.feedSort.value,
  });
  try {
    await fetchJson('/feed/add', {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: form,
    });
    elements.feedModal.close();
    elements.feedForm.reset();
    await loadGroups();
    await loadFeeds();
    setStatus('Feed added.');
  } catch (error) {
    setFormError(elements.feedFormError, error.message);
  } finally {
    elements.saveFeed.disabled = false;
  }
}

function openFeedSettings() {
	const feed = state.feeds.find(item => item.id === state.selectedFeedId);
	if (!feed) return;
	setFormError(elements.feedSettingsError);
	elements.feedSettingsName.textContent = feed.title || feed.url;
	elements.feedSettingsDisplay.value = feed.display_mode || 'headline';
	elements.feedSettingsSort.value = feed.sort_direction || 'desc';
	elements.feedSettingsModal.showModal();
}

async function saveFeedSettings() {
	const feed = state.feeds.find(item => item.id === state.selectedFeedId);
	if (!feed) return;
	setFormError(elements.feedSettingsError);
	elements.saveFeedSettings.disabled = true;
	const displayMode = elements.feedSettingsDisplay.value;
	const sortDirection = elements.feedSettingsSort.value;
	try {
		await fetchJson('/api/feeds/update', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({ feed_id: feed.id, display_mode: displayMode, sort_direction: sortDirection }),
		});
		feed.display_mode = displayMode;
		feed.sort_direction = sortDirection;
		elements.feedSettingsModal.close();
		await loadArticles(feed.id);
		setStatus('Feed settings saved.');
	} catch (error) {
		setFormError(elements.feedSettingsError, error.message);
	} finally {
		elements.saveFeedSettings.disabled = false;
	}
}

async function saveSettings() {
  setFormError(elements.settingsFormError);
  elements.saveSettings.disabled = true;
  const form = new URLSearchParams({
    refresh_interval_min: elements.settingsRefresh.value,
    max_articles_per_feed: elements.settingsMax.value,
    default_display_mode: elements.settingsDisplay.value,
    default_sort_order: elements.settingsSort.value,
    auto_refresh_enabled: elements.settingsAuto.checked ? 'true' : 'false',
	release_check_enabled: elements.settingsReleaseCheck.checked ? 'true' : 'false',
	release_check_include_prereleases: elements.settingsReleaseChannel.value === 'prerelease' ? 'true' : 'false',
  });
  try {
		const settings = await fetchJson('/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: form,
    });
		applySettings(settings);
    elements.settingsModal.close();
		await loadFeeds();
    setStatus('Settings saved.');
  } catch (error) {
    setFormError(elements.settingsFormError, error.message);
  } finally {
    elements.saveSettings.disabled = false;
  }
}

async function checkForNewRelease() {
	if (!state.releaseCheckEnabled) return;
	try {
		const result = await fetchJson('/api/releases/check');
		if (!result?.enabled || !result.update_available || !result.release) return;
		showReleaseModal(result);
	} catch {
		// Release checks should never interrupt reading feeds.
	}
}

function showReleaseModal(result) {
	const release = result.release;
	const releaseName = release.name || release.tag_name;
	elements.releaseSummary.textContent = `You are running ${result.current_version}. ${releaseName} is available.`;
	elements.releaseLink.href = release.html_url || result.releases_url || 'https://github.com/goosepod/feedss/releases';
	if (!document.querySelector('dialog[open]')) {
		elements.releaseModal.showModal();
	}
}

async function refreshNow() {
	setFormError(elements.settingsFormError);
	elements.settingsRefreshResult.hidden = true;
	elements.refreshNow.disabled = true;
	elements.refreshNow.textContent = 'Refreshing...';
	try {
		const result = await fetchJson('/api/refresh', { method: 'POST' });
		await loadGroups();
		await loadFeeds();
		const refreshed = result.refreshed ?? 0;
		const failed = result.failed ?? 0;
		elements.settingsRefreshResult.textContent = failed
			? `Updated ${refreshed} feed${refreshed === 1 ? '' : 's'}; ${failed} failed.`
			: `Updated ${refreshed} feed${refreshed === 1 ? '' : 's'}.`;
		elements.settingsRefreshResult.hidden = false;
	} catch (error) {
		setFormError(elements.settingsFormError, `Refresh failed: ${error.message}`);
	} finally {
		elements.refreshNow.disabled = false;
		elements.refreshNow.textContent = 'Refresh now';
	}
}

async function exportOpml() {
  try {
    const response = await request('/api/export-opml');
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'feedss.opml';
    link.click();
    URL.revokeObjectURL(url);
    setStatus('Feed list exported.');
  } catch (error) {
    setStatus(`Export failed: ${error.message}`, 'error');
  }
}

async function handleOpmlFileSelect(event) {
  const file = event.target.files?.[0];
  if (!file) return;
  const form = new FormData();
  form.append('file', file);
  setStatus('Importing feeds...');
  try {
    const result = await fetchJson('/api/import-opml', { method: 'POST', body: form });
    await loadGroups();
    await loadFeeds();
	const changed = (result.imported ?? 0) + (result.updated ?? 0);
	setStatus(`Placed ${changed} feed${changed === 1 ? '' : 's'} into their OPML groups.`);
  } catch (error) {
    setStatus(`Import failed: ${error.message}`, 'error');
  } finally {
    event.target.value = '';
  }
}

function moveFeed(offset) {
  const visibleFeeds = getVisibleFeeds();
  const index = visibleFeeds.findIndex(feed => feed.id === state.selectedFeedId);
  const next = visibleFeeds[index + offset];
  if (next) selectFeed(next.id);
}

function moveGroup(offset) {
  const index = state.groups.findIndex(group => group.id === state.selectedGroupId);
  const next = state.groups[index + offset];
  if (next) selectGroup(next.id);
}

function openSelectedArticleField(field) {
	let article = state.articles[state.selectedArticleIndex];
	if (!article && state.articles.length) {
		article = state.articles[0];
		activateArticle(article, 0);
	}
	const url = safeExternalURL(article?.[field]);
	if (url) window.open(url, '_blank', 'noopener');
}

function bindKeyboard() {
  document.addEventListener('keydown', event => {
    if (document.querySelector('dialog[open]')) return;
    if (event.target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(event.target.tagName)) return;
    const key = event.key.toLowerCase();
		if (event.key === '?') {
			event.preventDefault();
			return elements.shortcutsModal.showModal();
		}
		if (event.shiftKey && key === 'a') {
			event.preventDefault();
			return markAllRead();
		}
    if (event.shiftKey && key === 'j') return moveGroup(1);
    if (event.shiftKey && key === 'k') return moveGroup(-1);
    if (key === 'j') return moveArticle(1);
    if (key === 'k') return moveArticle(-1);
    if (key === 'n') return moveFeed(1);
    if (key === 'p') return moveFeed(-1);
		if (key === 'v') return openSelectedArticleField('link');
		if (key === 'c') return openSelectedArticleField('comments_link');
  });
}

function cacheElements() {
  Object.assign(elements, {
	status: document.getElementById('status'), subscriptionList: document.getElementById('subscription-list'),
	statusMessage: document.getElementById('status-message'),
	favicon: document.getElementById('favicon'),
	readerLabel: document.getElementById('reader-label'), feedHeader: document.getElementById('feed-header'),
	articlePane: document.getElementById('article-pane'), articleCount: document.getElementById('article-count'),
	markAllRead: document.getElementById('mark-all-read-btn'),
	feedSettingsButton: document.getElementById('feed-settings-btn'),
    feedModal: document.getElementById('feed-modal'), feedForm: document.getElementById('feed-form'),
    feedURL: document.getElementById('feed-url'), feedGroup: document.getElementById('feed-group'),
    feedDisplay: document.getElementById('feed-display-mode'), feedSort: document.getElementById('feed-sort-direction'),
    feedFormError: document.getElementById('feed-form-error'), saveFeed: document.getElementById('save-feed-btn'),
	feedSettingsModal: document.getElementById('feed-settings-modal'), feedSettingsForm: document.getElementById('feed-settings-form'),
	feedSettingsName: document.getElementById('feed-settings-name'), feedSettingsDisplay: document.getElementById('feed-settings-display-mode'),
	feedSettingsSort: document.getElementById('feed-settings-sort-direction'), feedSettingsError: document.getElementById('feed-settings-error'),
	saveFeedSettings: document.getElementById('save-feed-settings-btn'),
    settingsModal: document.getElementById('settings-modal'), settingsForm: document.getElementById('settings-form'),
    settingsRefresh: document.getElementById('settings-refresh-interval'), settingsMax: document.getElementById('settings-max-articles'),
	settingsDisplay: document.getElementById('settings-display-mode'), settingsSort: document.getElementById('settings-sort-order'),
	settingsAuto: document.getElementById('settings-auto-refresh'), settingsFormError: document.getElementById('settings-form-error'),
	settingsReleaseCheck: document.getElementById('settings-release-check'), settingsReleaseChannel: document.getElementById('settings-release-channel'),
	settingsRefreshResult: document.getElementById('settings-refresh-result'), refreshNow: document.getElementById('refresh-now-btn'),
	saveSettings: document.getElementById('save-settings-btn'), opmlInput: document.getElementById('opml-file-input'),
	shortcutsModal: document.getElementById('shortcuts-modal'),
	releaseModal: document.getElementById('release-modal'), releaseSummary: document.getElementById('release-modal-summary'),
	releaseLink: document.getElementById('release-modal-link'),
  });
}

document.addEventListener('DOMContentLoaded', async () => {
  cacheElements();
  document.getElementById('dismiss-status-btn').addEventListener('click', () => setStatus());
  document.getElementById('add-feed-btn').addEventListener('click', () => {
    setFormError(elements.feedFormError);
    elements.feedModal.showModal();
    elements.feedURL.focus();
  });
  document.getElementById('cancel-feed-btn').addEventListener('click', () => elements.feedModal.close());
  elements.feedForm.addEventListener('submit', event => { event.preventDefault(); saveFeed(); });
	elements.feedSettingsButton.addEventListener('click', openFeedSettings);
	document.getElementById('cancel-feed-settings-btn').addEventListener('click', () => elements.feedSettingsModal.close());
	elements.feedSettingsForm.addEventListener('submit', event => { event.preventDefault(); saveFeedSettings(); });
  document.getElementById('settings-btn').addEventListener('click', async () => {
    setFormError(elements.settingsFormError);
	elements.settingsRefreshResult.hidden = true;
    try {
      await loadSettings();
      elements.settingsModal.showModal();
    } catch (error) {
      setStatus(`Could not load settings: ${error.message}`, 'error');
    }
  });
  document.getElementById('cancel-settings-btn').addEventListener('click', () => elements.settingsModal.close());
  elements.settingsForm.addEventListener('submit', event => { event.preventDefault(); saveSettings(); });
	elements.refreshNow.addEventListener('click', refreshNow);
	document.getElementById('close-shortcuts-btn').addEventListener('click', () => elements.shortcutsModal.close());
	document.getElementById('dismiss-release-btn').addEventListener('click', () => elements.releaseModal.close());
  document.getElementById('import-btn').addEventListener('click', () => {
    elements.opmlInput.value = '';
    elements.opmlInput.click();
  });
  document.getElementById('export-btn').addEventListener('click', exportOpml);
  elements.opmlInput.addEventListener('change', handleOpmlFileSelect);
  elements.markAllRead.addEventListener('click', markAllRead);
  bindKeyboard();
	try {
		await loadSettings();
	} catch {
		// Reading remains available with built-in defaults if settings cannot be loaded.
	}
  try {
    await loadGroups();
    await loadFeeds();
  } catch (error) {
    setStatus(`Could not load feeds: ${error.message}`, 'error');
  }
	checkForNewRelease();
});
