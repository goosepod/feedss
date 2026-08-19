const ARTICLE_PAGE_SIZE = 30;
const FOREGROUND_SUBSCRIPTION_POLL_INTERVAL_MS = 30_000;
const BACKGROUND_SUBSCRIPTION_POLL_INTERVAL_MS = 15 * 60_000;
const DEFAULT_TITLE = 'feedss';
const STATIC_FAVICON = '/static/favicon.svg';

const state = {
  groups: [],
  feeds: [],
  articles: [],
	articleTotal: 0,
	articleOffset: 0,
	hasMoreArticles: false,
	hideReadArticles: false,
	articleSnapshotUnreadCount: 0,
	readThroughOrderIndex: null,
	readThroughID: null,
	articlesLoading: false,
	subscriptionMetadataLoading: false,
  selectedGroupId: null,
	selectedFeedId: null,
  viewMode: 'group',
	searchQuery: '',
	searchFeedId: null,
	searchGroupId: null,
	defaultDisplayMode: 'headline',
	defaultSortOrder: 'desc',
	releaseCheckEnabled: true,
	releaseCheckIncludePrereleases: false,
	selectedArticleIndex: -1,
  articleRequest: 0,
	expandedGroupIds: new Set(),
	expandedArticleIds: new Set(),
	feedPendingDeletion: null,
	editingFeedId: null,
};

if ('serviceWorker' in navigator) {
	window.addEventListener('load', () => {
		navigator.serviceWorker.register('/service-worker.js').catch(error => {
			console.warn('Service worker registration failed:', error);
		});
	});
}

const elements = {};
let subscriptionPollTimer = null;

function mobileMenuIsAvailable() {
	return window.matchMedia('(max-width: 760px)').matches;
}

function closeMobileMenus() {
	document.body.classList.remove('mobile-nav-open', 'mobile-actions-open', 'search-open');
	elements.mobileNavToggle?.setAttribute('aria-expanded', 'false');
	elements.mobileActionsToggle?.setAttribute('aria-expanded', 'false');
	elements.searchViewButton?.setAttribute('aria-expanded', 'false');
}

function toggleMobileMenu(menu) {
	if (!mobileMenuIsAvailable()) return;
	const className = menu === 'nav' ? 'mobile-nav-open' : 'mobile-actions-open';
	const willOpen = !document.body.classList.contains(className);
	closeMobileMenus();
	if (!willOpen) return;
	document.body.classList.add(className);
	const toggle = menu === 'nav' ? elements.mobileNavToggle : elements.mobileActionsToggle;
	toggle?.setAttribute('aria-expanded', 'true');
	requestAnimationFrame(() => {
		if (menu === 'nav') {
			document.getElementById('mobile-nav-close')?.focus();
		} else {
			elements.appActions?.querySelector('button:not([hidden]), a:not([hidden])')?.focus();
		}
	});
}

function toggleSearch() {
	const willOpen = !document.body.classList.contains('search-open');
	closeMobileMenus();
	if (!willOpen) return;
	document.body.classList.add('search-open');
	elements.searchViewButton.setAttribute('aria-expanded', 'true');
	requestAnimationFrame(() => elements.articleSearch.focus());
}

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

async function loadUsers() {
	if (!elements.userList) return;
	const users = await fetchJson('/api/users');
	elements.userList.replaceChildren();
	users.forEach(user => {
		const row = document.createElement('div');
		row.className = 'user-row';
		const name = document.createElement('span');
		name.textContent = user.username;
		const stateLabel = document.createElement('span');
		stateLabel.className = 'user-state';
		stateLabel.textContent = user.is_admin ? 'Administrator' : (user.must_change_password ? 'Temporary password' : 'Active');
		row.append(name, stateLabel);
		elements.userList.appendChild(row);
	});
}

async function addUserAccount() {
	setFormError(elements.newUserError);
	const username = elements.newUserUsername.value.trim();
	const temporaryPassword = elements.newUserPassword.value;
	if (!username || !temporaryPassword) {
		setFormError(elements.newUserError, 'Username and temporary password are required.');
		return;
	}
	elements.addUserAccount.disabled = true;
	try {
		await fetchJson('/api/users', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({ username, temporary_password: temporaryPassword }),
		});
		elements.newUserUsername.value = '';
		elements.newUserPassword.value = '';
		await loadUsers();
		setStatus(`${username} can now sign in with their temporary password.`);
	} catch (error) {
		setFormError(elements.newUserError, error.message);
	} finally {
		elements.addUserAccount.disabled = false;
	}
}

async function openAccount() {
	setFormError(elements.accountError);
	elements.accountCurrentPassword.value = '';
	elements.accountNewPassword.value = '';
	elements.accountConfirmPassword.value = '';
	try {
		const account = await fetchJson('/api/account');
		elements.accountUsername.value = account.username || '';
		elements.accountModal.showModal();
		elements.accountUsername.focus();
	} catch (error) {
		setStatus(`Could not load account: ${error.message}`, 'error');
	}
}

async function saveAccount() {
	setFormError(elements.accountError);
	const username = elements.accountUsername.value.trim();
	const currentPassword = elements.accountCurrentPassword.value;
	const newPassword = elements.accountNewPassword.value;
	const confirmation = elements.accountConfirmPassword.value;
	if (newPassword !== confirmation) {
		setFormError(elements.accountError, 'New passwords do not match.');
		return;
	}
	elements.saveAccount.disabled = true;
	try {
		await fetchJson('/api/account', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({
				username, current_password: currentPassword, new_password: newPassword, confirm_password: confirmation,
			}),
		});
		elements.accountModal.close();
		setStatus('Account updated.');
	} catch (error) {
		setFormError(elements.accountError, error.message);
	} finally {
		elements.saveAccount.disabled = false;
	}
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
		const group = state.groups.find(item => item.id === state.selectedGroupId);
		await loadGroupArticles(state.selectedGroupId, { hideRead: sourceHasUnread(group) });
	} else if (state.selectedFeedId) {
		const feed = state.feeds.find(item => item.id === state.selectedFeedId);
		await loadArticles(state.selectedFeedId, { hideRead: sourceHasUnread(feed) });
	} else {
    state.articles = [];
	state.articleTotal = 0;
	state.articleOffset = 0;
    renderArticles();
  }
}

async function refreshSubscriptionMetadata() {
	if (state.subscriptionMetadataLoading) return;
	state.subscriptionMetadataLoading = true;
	try {
		const [groups, feeds] = await Promise.all([
			fetchJson('/api/groups'),
			fetchJson('/api/feeds'),
		]);
		state.groups = Array.isArray(groups) ? groups : [];
		state.feeds = Array.isArray(feeds) ? feeds : [];
		state.expandedGroupIds = new Set(
			[...state.expandedGroupIds].filter(id => state.groups.some(group => group.id === id)),
		);
		renderSubscriptions();
		updateArticleControls();
	} catch {
		// Background metadata polling should never interrupt reading.
	} finally {
		state.subscriptionMetadataLoading = false;
	}
}

function subscriptionMetadataPollInterval(background = document.hidden || !document.hasFocus()) {
	return background
		? BACKGROUND_SUBSCRIPTION_POLL_INTERVAL_MS
		: FOREGROUND_SUBSCRIPTION_POLL_INTERVAL_MS;
}

function scheduleSubscriptionMetadataPoll() {
	if (subscriptionPollTimer !== null) window.clearTimeout(subscriptionPollTimer);
	subscriptionPollTimer = window.setTimeout(async () => {
		await refreshSubscriptionMetadata();
		scheduleSubscriptionMetadataPoll();
	}, subscriptionMetadataPollInterval());
}

function startSubscriptionMetadataPolling() {
	const handleAttentionChange = () => {
		const focused = !document.hidden && document.hasFocus();
		scheduleSubscriptionMetadataPoll();
		if (focused) void refreshSubscriptionMetadata();
	};
	scheduleSubscriptionMetadataPoll();
	window.addEventListener('focus', handleAttentionChange);
	window.addEventListener('blur', scheduleSubscriptionMetadataPoll);
	document.addEventListener('visibilitychange', handleAttentionChange);
}

function captureArticleSnapshot(data, articles, source, hideRead) {
	const explicitOrder = Number(data?.read_through_order_index);
	const explicitID = Number(data?.read_through_id);
	let boundary = Number.isFinite(explicitOrder) && Number.isFinite(explicitID)
		? { order_index: explicitOrder, id: explicitID }
		: null;
	if (!boundary && articles.length) {
		boundary = {
			order_index: Math.max(...articles.map(article => Number(article.order_index))),
			id: Math.max(...articles.map(article => Number(article.id))),
		};
	}
	state.readThroughOrderIndex = boundary?.order_index ?? null;
	state.readThroughID = boundary?.id ?? null;
	state.articleSnapshotUnreadCount = hideRead
		? state.articleTotal
		: (Number(source?.unread_count) || articles.filter(article => !article.is_read).length);
}

function reconcileVisibleUnreadCount(source) {
	if (!state.hideReadArticles || !source || state.articleTotal <= (Number(source.unread_count) || 0)) return;
	const increase = state.articleTotal - (Number(source.unread_count) || 0);
	source.unread_count = state.articleTotal;
	if (state.viewMode === 'feed') {
		const group = state.groups.find(item => item.id === source.group_id);
		if (group) group.unread_count = (Number(group.unread_count) || 0) + increase;
	}
	renderSubscriptions();
}

async function loadGroupArticles(groupID, { hideRead = false } = {}) {
	const requestID = ++state.articleRequest;
	const group = state.groups.find(item => item.id === groupID);
	elements.readerLabel.textContent = 'Current group';
	elements.feedHeader.textContent = group?.name || 'Loading group';
	state.articleTotal = 0;
	state.articleOffset = 0;
	state.hasMoreArticles = false;
	state.hideReadArticles = hideRead;
	state.readThroughOrderIndex = null;
	state.readThroughID = null;
	state.articleSnapshotUnreadCount = 0;
	elements.articlePane.innerHTML = '<div class="empty-state">Loading articles...</div>';
	updateArticleControls();
	try {
		const unreadOnly = hideRead ? '&unread_only=1' : '';
		const data = await fetchJson(`/api/articles?group_id=${encodeURIComponent(groupID)}&limit=${ARTICLE_PAGE_SIZE}${unreadOnly}`);
		if (requestID !== state.articleRequest) return;
		state.articles = Array.isArray(data?.articles) ? data.articles : [];
		state.articleOffset = state.articles.length;
		state.articleTotal = Number.isFinite(data?.total) ? data.total : state.articles.length;
		captureArticleSnapshot(data, state.articles, group, hideRead);
		state.hasMoreArticles = typeof data?.has_more === 'boolean'
			? data.has_more : state.articles.length < state.articleTotal;
		state.selectedArticleIndex = -1;
		state.expandedArticleIds = new Set(
			state.defaultDisplayMode === 'headline' ? [] : state.articles.map(article => article.id),
		);
		elements.articlePane.scrollTop = 0;
		reconcileVisibleUnreadCount(group);
		renderArticles();
	} catch (error) {
		if (requestID !== state.articleRequest) return;
		state.articles = [];
		state.articleTotal = 0;
		state.articleOffset = 0;
		state.hasMoreArticles = false;
		renderArticles();
		setStatus(`Could not load group articles: ${error.message}`, 'error');
	}
}

async function loadArticles(feedID, { hideRead = false } = {}) {
  const requestID = ++state.articleRequest;
  const feed = state.feeds.find(item => item.id === feedID);
	elements.readerLabel.textContent = 'Current feed';
  elements.feedHeader.textContent = feed?.title || 'Loading feed';
	state.articleTotal = 0;
	state.articleOffset = 0;
	state.hasMoreArticles = false;
	state.hideReadArticles = hideRead;
	state.readThroughOrderIndex = null;
	state.readThroughID = null;
	state.articleSnapshotUnreadCount = 0;
  elements.articlePane.innerHTML = '<div class="empty-state">Loading articles...</div>';
  updateArticleControls();
  try {
    const unreadOnly = hideRead ? '&unread_only=1' : '';
    const data = await fetchJson(`/api/articles?feed_id=${encodeURIComponent(feedID)}&limit=${ARTICLE_PAGE_SIZE}${unreadOnly}`);
    if (requestID !== state.articleRequest) return;
	state.articles = Array.isArray(data?.articles) ? data.articles : [];
	state.articleOffset = state.articles.length;
	state.articleTotal = Number.isFinite(data?.total) ? data.total : state.articles.length;
	captureArticleSnapshot(data, state.articles, feed, hideRead);
	state.hasMoreArticles = typeof data?.has_more === 'boolean'
		? data.has_more : state.articles.length < state.articleTotal;
		state.selectedArticleIndex = -1;
	state.expandedArticleIds = new Set(
	  feed?.display_mode === 'headline' ? [] : state.articles.map(article => article.id),
	);
	elements.articlePane.scrollTop = 0;
	reconcileVisibleUnreadCount(feed);
    renderArticles();
  } catch (error) {
    if (requestID !== state.articleRequest) return;
    state.articles = [];
	state.articleTotal = 0;
	state.articleOffset = 0;
	state.hasMoreArticles = false;
    renderArticles();
    setStatus(`Could not load articles: ${error.message}`, 'error');
  }
}

async function loadSavedArticles() {
	const requestID = ++state.articleRequest;
	elements.readerLabel.textContent = 'Library';
	elements.feedHeader.textContent = 'Saved articles';
	state.articleTotal = 0;
	state.articleOffset = 0;
	state.hasMoreArticles = false;
	state.hideReadArticles = false;
	elements.articlePane.innerHTML = '<div class="empty-state">Loading saved articles...</div>';
	updateArticleControls();
	try {
		const data = await fetchJson(`/api/articles?saved=1&limit=${ARTICLE_PAGE_SIZE}`);
		if (requestID !== state.articleRequest) return;
		state.articles = Array.isArray(data?.articles) ? data.articles : [];
		state.articleOffset = state.articles.length;
		state.articleTotal = Number.isFinite(data?.total) ? data.total : state.articles.length;
		state.hasMoreArticles = typeof data?.has_more === 'boolean' ? data.has_more : state.articles.length < state.articleTotal;
		state.selectedArticleIndex = -1;
		state.expandedArticleIds = new Set();
		elements.articlePane.scrollTop = 0;
		renderArticles();
	} catch (error) {
		if (requestID !== state.articleRequest) return;
		state.articles = [];
		state.articleTotal = 0;
		state.hasMoreArticles = false;
		renderArticles();
		setStatus(`Could not load saved articles: ${error.message}`, 'error');
	}
}

function searchScopeParameters() {
	if (state.searchFeedId !== null) return `&feed_id=${encodeURIComponent(state.searchFeedId)}`;
	if (state.searchGroupId !== null) return `&group_id=${encodeURIComponent(state.searchGroupId)}`;
	return '';
}

async function loadSearchResults() {
	const requestID = ++state.articleRequest;
	elements.readerLabel.textContent = state.searchFeedId !== null
		? 'Search in feed' : (state.searchGroupId !== null ? 'Search in group' : 'Search all articles');
	elements.feedHeader.textContent = `Results for “${state.searchQuery}”`;
	state.articleTotal = 0;
	state.articleOffset = 0;
	state.hasMoreArticles = false;
	state.hideReadArticles = false;
	elements.articlePane.innerHTML = '<div class="empty-state">Searching articles...</div>';
	updateArticleControls();
	try {
		const data = await fetchJson(`/api/search?q=${encodeURIComponent(state.searchQuery)}&limit=${ARTICLE_PAGE_SIZE}${searchScopeParameters()}`);
		if (requestID !== state.articleRequest) return;
		state.articles = Array.isArray(data?.articles) ? data.articles : [];
		state.articleOffset = state.articles.length;
		state.articleTotal = Number.isFinite(data?.total) ? data.total : state.articles.length;
		state.hasMoreArticles = typeof data?.has_more === 'boolean' ? data.has_more : state.articles.length < state.articleTotal;
		state.selectedArticleIndex = -1;
		state.expandedArticleIds = new Set();
		elements.articlePane.scrollTop = 0;
		renderArticles();
	} catch (error) {
		if (requestID !== state.articleRequest) return;
		state.articles = [];
		state.articleTotal = 0;
		state.hasMoreArticles = false;
		renderArticles();
		setStatus(`Could not search articles: ${error.message}`, 'error');
	}
}

function selectSavedArticles() {
	state.selectedGroupId = null;
	state.selectedFeedId = null;
	state.viewMode = 'saved';
	state.articles = [];
	renderSubscriptions();
	void loadSavedArticles();
}

function submitSearch() {
	const query = elements.articleSearch.value.trim();
	if (!query) return;
	const currentScope = elements.searchScope.querySelector('input:checked')?.value === 'current';
	state.searchFeedId = currentScope && state.viewMode === 'feed' ? state.selectedFeedId : null;
	state.searchGroupId = currentScope && state.viewMode === 'group' ? state.selectedGroupId : null;
	state.searchQuery = query;
	state.selectedGroupId = null;
	state.selectedFeedId = null;
	state.viewMode = 'search';
	state.articles = [];
	closeMobileMenus();
	renderSubscriptions();
	void loadSearchResults();
}

function getVisibleFeeds() {
  return state.selectedGroupId === null
    ? state.feeds
    : state.feeds.filter(feed => feed.group_id === state.selectedGroupId);
}

function sourceHasUnread(source) {
	return (Number(source?.unread_count) || 0) > 0;
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
	elements.savedViewButton.classList.toggle('active', state.viewMode === 'saved');
	if (state.viewMode === 'saved') elements.savedViewButton.setAttribute('aria-current', 'page');
	else elements.savedViewButton.removeAttribute('aria-current');
	elements.searchViewButton.classList.toggle('active', state.viewMode === 'search');
	if (state.viewMode === 'search') elements.searchViewButton.setAttribute('aria-current', 'page');
	else elements.searchViewButton.removeAttribute('aria-current');
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
		const feedButton = createNavButton(
          'feed-item', feed.title || feed.url, feed.id === state.selectedFeedId,
          () => selectFeed(feed.id), feed.unread_count || 0,
		);
		if (feed.last_refresh_error) {
			feedButton.classList.add('has-error');
			const warning = document.createElement('span');
			warning.className = 'feed-warning';
			warning.textContent = '⚠';
			warning.setAttribute('aria-label', 'Update failed');
			warning.title = feed.last_refresh_error;
			feedButton.insertBefore(warning, feedButton.lastChild);
		}
		feedList.appendChild(feedButton);
      }
      groupElement.appendChild(feedList);
    }
    elements.subscriptionList.appendChild(groupElement);
  }
	if (sidebar) sidebar.scrollTop = scrollTop;
	const problemCount = state.feeds.filter(feed => feed.last_refresh_error).length;
	elements.problemFeedsButton.hidden = problemCount === 0;
	elements.problemFeedsButton.textContent = problemCount === 1 ? '1 problem feed' : `${problemCount} problem feeds`;
	updateBrowserUnreadBadge();
}

function totalUnreadCount() {
	return state.groups.reduce((sum, group) => sum + (Number(group.unread_count) || 0), 0);
}

function formatFaviconUnreadCount(count) {
	if (count < 1000) return String(count);
	const divisor = count < 1_000_000 ? 1000 : 1_000_000;
	const suffix = count < 1_000_000 ? 'k' : 'm';
	const scaled = count / divisor;
	const compact = scaled < 10 ? Number(scaled.toFixed(1)) : Math.floor(scaled);
	return `${compact}${suffix}`;
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
	const group = state.groups.find(item => item.id === groupID);
  state.selectedGroupId = groupID;
	state.selectedFeedId = null;
	state.viewMode = 'group';
  state.articles = [];
	state.articleTotal = 0;
	state.articleOffset = 0;
	state.hasMoreArticles = false;
  renderSubscriptions();
	loadGroupArticles(groupID, { hideRead: sourceHasUnread(group) });
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
  const feed = state.feeds.find(item => item.id === feedID);
  if (feed) {
    state.selectedGroupId = feed.group_id;
  }
  state.selectedFeedId = feedID;
	state.viewMode = 'feed';
  renderSubscriptions();
  loadArticles(feedID, { hideRead: sourceHasUnread(feed) });
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

async function toggleArticleSaved(article) {
	const wasSaved = Boolean(article.is_saved);
	article.is_saved = !wasSaved;
	if (state.viewMode === 'saved' && wasSaved) {
		state.articles = state.articles.filter(item => item.id !== article.id);
		state.articleTotal = Math.max(0, state.articleTotal - 1);
		state.selectedArticleIndex = -1;
	}
	renderArticles();
	try {
		await fetchJson('/api/articles/saved', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({ article_id: article.id, saved: article.is_saved }),
		});
	} catch (error) {
		article.is_saved = wasSaved;
		if (state.viewMode === 'saved' && wasSaved && !state.articles.some(item => item.id === article.id)) {
			state.articles.push(article);
			state.articleTotal += 1;
		}
		renderArticles();
		setStatus(`Could not update saved article: ${error.message}`, 'error');
	}
}

function renderArticles() {
  const feed = state.feeds.find(item => item.id === state.selectedFeedId);
	const group = state.groups.find(item => item.id === state.selectedGroupId);
	const source = state.viewMode === 'group' ? group : feed;
	if (state.viewMode === 'saved') elements.feedHeader.textContent = 'Saved articles';
	else if (state.viewMode === 'search') elements.feedHeader.textContent = `Results for “${state.searchQuery}”`;
	else elements.feedHeader.textContent = source?.name || source?.title || 'Select a subscription';
  elements.articlePane.replaceChildren();
	if (!source && !['saved', 'search'].includes(state.viewMode)) {
		elements.articlePane.innerHTML = '<div class="empty-state">Add or choose a subscription to start reading.</div>';
    updateArticleControls();
    return;
  }
  if (!state.articles.length) {
		const message = state.viewMode === 'saved'
			? 'No saved articles yet. Use the star on an article to keep it here.'
			: (state.viewMode === 'search' ? 'No articles matched this search.' : 'No articles are available here yet.');
		elements.articlePane.innerHTML = `<div class="empty-state">${message}</div>`;
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
		const headingRow = document.createElement('div');
		headingRow.className = 'article-heading-row';
		const saveButton = document.createElement('button');
		saveButton.type = 'button';
		saveButton.className = `save-article${article.is_saved ? ' saved' : ''}`;
		saveButton.textContent = article.is_saved ? '★' : '☆';
		saveButton.setAttribute('aria-label', `${article.is_saved ? 'Remove from saved' : 'Save'}: ${article.title || 'Untitled article'}`);
		saveButton.title = article.is_saved ? 'Remove from saved' : 'Save article';
		saveButton.addEventListener('click', event => {
			event.stopPropagation();
			void toggleArticleSaved(article);
		});
		headingRow.append(title, saveButton);
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
		header.append(headingRow, meta);
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
  updateArticleControls();
	queueMicrotask(maybeLoadMoreArticles);
}

function updateArticleControls() {
	const total = state.articleTotal;
	const source = state.viewMode === 'group'
		? state.groups.find(item => item.id === state.selectedGroupId)
		: state.feeds.find(item => item.id === state.selectedFeedId);
	const unread = source?.unread_count ?? 0;
	elements.articleCount.textContent = `${total} article${total === 1 ? '' : 's'}`;
	elements.markAllRead.disabled = !source || unread === 0;
	elements.feedSettingsButton.hidden = state.viewMode !== 'feed' || state.selectedFeedId === null;
}

async function moveArticle(offset) {
  if (!state.articles.length) return;
	if (state.selectedArticleIndex < 0 && offset < 0) return;
	if (offset > 0 && state.selectedArticleIndex === state.articles.length - 1 && state.hasMoreArticles) {
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
	if (state.articlesLoading || !state.hasMoreArticles || !state.articles.length) return false;
	state.articlesLoading = true;
	const requestID = state.articleRequest;
	let url;
	if (state.viewMode === 'saved') {
		url = `/api/articles?saved=1&limit=${ARTICLE_PAGE_SIZE}&offset=${state.articleOffset}`;
	} else if (state.viewMode === 'search') {
		url = `/api/search?q=${encodeURIComponent(state.searchQuery)}&limit=${ARTICLE_PAGE_SIZE}&offset=${state.articleOffset}${searchScopeParameters()}`;
	} else {
		const parameter = state.viewMode === 'group' ? 'group_id' : 'feed_id';
		const id = state.viewMode === 'group' ? state.selectedGroupId : state.selectedFeedId;
		const lastArticle = state.articles[state.articles.length - 1];
		const cursor = Number.isFinite(Number(lastArticle.order_index))
			? `&cursor_order_index=${encodeURIComponent(lastArticle.order_index)}&cursor_id=${encodeURIComponent(lastArticle.id)}`
			: `&offset=${state.articleOffset}`;
		const unreadOnly = state.hideReadArticles ? '&unread_only=1' : '';
		url = `/api/articles?${parameter}=${encodeURIComponent(id)}&limit=${ARTICLE_PAGE_SIZE}${cursor}${unreadOnly}`;
	}
	const scrollTop = elements.articlePane.scrollTop;
	try {
		const data = await fetchJson(url);
		if (requestID !== state.articleRequest) return false;
		const received = Array.isArray(data?.articles) ? data.articles : [];
		state.articleOffset += received.length;
		const existingIDs = new Set(state.articles.map(article => article.id));
		const articles = received.filter(article => !existingIDs.has(article.id));
		state.articles.push(...articles);
		state.hasMoreArticles = typeof data?.has_more === 'boolean'
			? data.has_more : received.length === ARTICLE_PAGE_SIZE;
		if (getCurrentDisplayMode() !== 'headline') {
			articles.forEach(article => state.expandedArticleIds.add(article.id));
		}
		return articles.length > 0;
	} catch (error) {
		setStatus(`Could not load more articles: ${error.message}`, 'error');
		return false;
	} finally {
		state.articlesLoading = false;
		if (requestID === state.articleRequest) {
			renderArticles();
			elements.articlePane.scrollTop = scrollTop;
		}
	}
}

function maybeLoadMoreArticles() {
	if (state.articlesLoading || !state.hasMoreArticles) return;
	const remaining = elements.articlePane.scrollHeight - elements.articlePane.scrollTop - elements.articlePane.clientHeight;
	if (remaining <= Math.max(400, elements.articlePane.clientHeight * 0.75)) void loadMoreArticles();
}

async function refreshCurrentView() {
	elements.articlePane.scrollTop = 0;
	if (state.viewMode === 'group' && state.selectedGroupId !== null) {
		await loadGroupArticles(state.selectedGroupId, { hideRead: true });
	} else if (state.viewMode === 'feed' && state.selectedFeedId !== null) {
		await loadArticles(state.selectedFeedId, { hideRead: true });
	} else if (state.viewMode === 'saved') {
		await loadSavedArticles();
	} else if (state.viewMode === 'search') {
		await loadSearchResults();
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
		if (feed) adjustUnreadCounts(feed, 1);
		articleElement?.classList.remove('read');
		articleElement?.classList.add('unread');
		if (articleElement?.querySelector('.article-content')) renderArticles();
		setStatus(`Could not mark article read: ${error.message}`, 'error');
	}
}

function requestMarkAllRead() {
	const source = state.viewMode === 'group'
		? state.groups.find(item => item.id === state.selectedGroupId)
		: state.feeds.find(item => item.id === state.selectedFeedId);
	if (!source || (Number(source.unread_count) || 0) === 0) return;
	const name = source.name || source.title || 'the current view';
	elements.markAllReadSummary.textContent = `Mark unread articles currently in ${name} as read? Newer articles won't be affected.`;
	elements.markAllReadModal.showModal();
}

async function markAllRead() {
	const mode = state.viewMode;
	const hideRead = state.hideReadArticles;
	const feed = state.feeds.find(item => item.id === state.selectedFeedId);
	const group = state.groups.find(item => item.id === state.selectedGroupId);
	if (mode === 'feed' && !feed) return;
	if (mode === 'group' && !group) return;
	const unread = state.articles.filter(article => !article.is_read);
	if ((mode === 'group' ? group.unread_count : feed.unread_count) === 0 && !unread.length) return;
	if (state.readThroughOrderIndex === null || state.readThroughID === null) {
		await refreshCurrentView();
		return;
	}
	const previousGroupUnread = group?.unread_count || 0;
	const previousFeedUnread = new Map(state.feeds.map(item => [item.id, item.unread_count || 0]));
	unread.forEach(article => { article.is_read = true; });
	if (mode === 'group') {
		const loadedUnreadByFeed = new Map();
		unread.forEach(article => loadedUnreadByFeed.set(article.feed_id, (loadedUnreadByFeed.get(article.feed_id) || 0) + 1));
		state.feeds.filter(item => item.group_id === group.id).forEach(item => {
			item.unread_count = Math.max(0, (Number(item.unread_count) || 0) - (loadedUnreadByFeed.get(item.id) || 0));
		});
		group.unread_count = Math.max(0, previousGroupUnread - state.articleSnapshotUnreadCount);
		renderSubscriptions();
	} else {
		const groupForFeed = state.groups.find(item => item.id === feed.group_id);
		const markedCount = Math.min(Number(feed.unread_count) || 0, state.articleSnapshotUnreadCount);
		if (groupForFeed) groupForFeed.unread_count = Math.max(0, groupForFeed.unread_count - markedCount);
		feed.unread_count = Math.max(0, (Number(feed.unread_count) || 0) - markedCount);
		renderSubscriptions();
	}
	renderArticles();
	try {
		const body = new URLSearchParams(mode === 'group' ? { group_id: group.id } : { feed_id: feed.id });
		body.set('read_through_order_index', state.readThroughOrderIndex);
		body.set('read_through_id', state.readThroughID);
		await fetchJson('/api/articles/read', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body,
		});
		if (mode === 'group') await loadGroupArticles(group.id, { hideRead });
		else await loadArticles(feed.id, { hideRead });
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

function openFeedSettings(feedOverride = null) {
	const feed = feedOverride || state.feeds.find(item => item.id === state.selectedFeedId);
	if (!feed) return;
	state.editingFeedId = feed.id;
	setFormError(elements.feedSettingsError);
	elements.feedSettingsName.textContent = feed.title || feed.url;
	elements.feedSettingsURL.value = feed.url || '';
	elements.feedSettingsDisplay.value = feed.display_mode || 'headline';
	elements.feedSettingsSort.value = feed.sort_direction || 'desc';
	elements.feedSettingsModal.showModal();
}

async function saveFeedSettings() {
	const feed = state.feeds.find(item => item.id === state.editingFeedId);
	if (!feed) return;
	setFormError(elements.feedSettingsError);
	elements.saveFeedSettings.disabled = true;
	const displayMode = elements.feedSettingsDisplay.value;
	const sortDirection = elements.feedSettingsSort.value;
	const feedURL = elements.feedSettingsURL.value.trim();
	try {
		await fetchJson('/api/feeds/update', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({ feed_id: feed.id, url: feedURL, display_mode: displayMode, sort_direction: sortDirection }),
		});
		feed.url = feedURL;
		feed.display_mode = displayMode;
		feed.sort_direction = sortDirection;
		elements.feedSettingsModal.close();
		if (feed.id === state.selectedFeedId) await loadArticles(feed.id, { hideRead: state.hideReadArticles });
		setStatus('Feed settings saved.');
	} catch (error) {
		setFormError(elements.feedSettingsError, error.message);
	} finally {
		elements.saveFeedSettings.disabled = false;
	}
}

function failedFeeds() {
	return state.feeds.filter(feed => feed.last_refresh_error);
}

function formatRefreshTime(rawTime) {
	const date = rawTime ? new Date(rawTime) : null;
	return date && !Number.isNaN(date.valueOf()) ? date.toLocaleString() : 'Unknown time';
}

function renderProblemFeeds() {
	const feeds = failedFeeds();
	elements.problemFeedsList.replaceChildren();
	if (!feeds.length) {
		elements.problemFeedsList.innerHTML = '<p class="nav-empty">No feeds currently have update problems.</p>';
		return;
	}
	feeds.forEach(feed => {
		const item = document.createElement('article');
		item.className = 'problem-feed';
		const title = document.createElement('h3');
		title.textContent = feed.title || 'Untitled feed';
		const feedURL = document.createElement('p');
		feedURL.className = 'problem-feed-url';
		feedURL.textContent = feed.url;
		const error = document.createElement('p');
		error.className = 'problem-feed-error';
		error.textContent = feed.last_refresh_error;
		const attempted = document.createElement('p');
		attempted.className = 'problem-feed-time';
		attempted.textContent = `Last attempted ${formatRefreshTime(feed.last_refresh_at)}`;
		const actions = document.createElement('div');
		actions.className = 'problem-feed-actions';
		const retry = document.createElement('button');
		retry.type = 'button';
		retry.className = 'button-secondary';
		retry.textContent = 'Retry';
		retry.addEventListener('click', () => retryFeed(feed, retry));
		const edit = document.createElement('button');
		edit.type = 'button';
		edit.className = 'button-secondary';
		edit.textContent = 'Edit';
		edit.addEventListener('click', () => {
			elements.problemFeedsModal.close();
			openFeedSettings(feed);
		});
		const remove = document.createElement('button');
		remove.type = 'button';
		remove.className = 'button-danger';
		remove.textContent = 'Remove';
		remove.addEventListener('click', () => requestDeleteFeed(feed));
		actions.append(retry, edit, remove);
		item.append(title, feedURL, error, attempted, actions);
		elements.problemFeedsList.appendChild(item);
	});
}

function openProblemFeeds() {
	renderProblemFeeds();
	elements.problemFeedsModal.showModal();
}

async function retryFeed(feed, button) {
	button.disabled = true;
	button.textContent = 'Retrying...';
	try {
		const result = await fetchJson('/api/refresh', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({ feed_id: feed.id }),
		});
		await loadGroups();
		await loadFeeds();
		renderProblemFeeds();
		if (result.failed) setStatus(`${feed.title || 'Feed'} still could not be updated.`, 'error');
		else setStatus(`${feed.title || 'Feed'} updated successfully.`);
	} catch (error) {
		setStatus(`Could not retry feed: ${error.message}`, 'error');
		button.disabled = false;
		button.textContent = 'Retry';
	}
}

function requestDeleteFeed(feed) {
	state.feedPendingDeletion = feed.id;
	elements.problemFeedsModal.close();
	elements.deleteFeedSummary.textContent = `Remove ${feed.title || feed.url}?`;
	setFormError(elements.deleteFeedError);
	elements.deleteFeedModal.showModal();
}

async function deletePendingFeed() {
	const feed = state.feeds.find(item => item.id === state.feedPendingDeletion);
	if (!feed) return;
	elements.confirmDeleteFeed.disabled = true;
	setFormError(elements.deleteFeedError);
	try {
		await fetchJson('/api/feeds/delete', {
			method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({ feed_id: feed.id }),
		});
		if (state.selectedFeedId === feed.id) state.selectedFeedId = null;
		state.feedPendingDeletion = null;
		elements.deleteFeedModal.close();
		await loadGroups();
		await loadFeeds();
		setStatus(`${feed.title || 'Feed'} removed.`);
	} catch (error) {
		setFormError(elements.deleteFeedError, error.message);
	} finally {
		elements.confirmDeleteFeed.disabled = false;
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

function toggleSelectedArticleSaved() {
	const article = state.articles[state.selectedArticleIndex] || state.articles[0];
	if (article) void toggleArticleSaved(article);
}

function bindKeyboard() {
  document.addEventListener('keydown', event => {
		if (event.key === 'Escape' && (document.body.classList.contains('mobile-nav-open') || document.body.classList.contains('mobile-actions-open') || document.body.classList.contains('search-open'))) {
			event.preventDefault();
			closeMobileMenus();
			return;
		}
    if (document.querySelector('dialog[open]')) return;
    if (event.target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(event.target.tagName)) return;
    const key = event.key.toLowerCase();
		if (event.key === '?') {
			event.preventDefault();
			return elements.shortcutsModal.showModal();
		}
		if (event.shiftKey && key === 'a') {
			event.preventDefault();
			return requestMarkAllRead();
		}
    if (event.shiftKey && key === 'j') return moveGroup(1);
    if (event.shiftKey && key === 'k') return moveGroup(-1);
    if (key === 'j') return moveArticle(1);
    if (key === 'k') return moveArticle(-1);
    if (key === 'n') return moveFeed(1);
    if (key === 'p') return moveFeed(-1);
		if (key === 'v') return openSelectedArticleField('link');
		if (key === 'c') return openSelectedArticleField('comments_link');
		if (key === 's') {
			event.preventDefault();
			return toggleSelectedArticleSaved();
		}
		if (key === 'r') {
			event.preventDefault();
			return refreshCurrentView();
		}
  });
}

function cacheElements() {
  Object.assign(elements, {
	status: document.getElementById('status'), subscriptionList: document.getElementById('subscription-list'),
	mobileNavToggle: document.getElementById('mobile-nav-toggle'), mobileActionsToggle: document.getElementById('mobile-actions-toggle'),
	mobileMenuBackdrop: document.getElementById('mobile-menu-backdrop'), appActions: document.getElementById('app-actions'),
	statusMessage: document.getElementById('status-message'),
	favicon: document.getElementById('favicon'),
	readerLabel: document.getElementById('reader-label'), feedHeader: document.getElementById('feed-header'),
	savedViewButton: document.getElementById('saved-view-btn'), searchForm: document.getElementById('search-form'),
	searchViewButton: document.getElementById('search-view-btn'),
	articleSearch: document.getElementById('article-search'), searchScope: document.getElementById('search-scope'),
	articlePane: document.getElementById('article-pane'), articleCount: document.getElementById('article-count'),
	markAllRead: document.getElementById('mark-all-read-btn'),
	markAllReadModal: document.getElementById('mark-all-read-modal'),
	markAllReadSummary: document.getElementById('mark-all-read-modal-summary'),
	confirmMarkAllRead: document.getElementById('confirm-mark-all-read-btn'),
	feedSettingsButton: document.getElementById('feed-settings-btn'),
    feedModal: document.getElementById('feed-modal'), feedForm: document.getElementById('feed-form'),
    feedURL: document.getElementById('feed-url'), feedGroup: document.getElementById('feed-group'),
    feedDisplay: document.getElementById('feed-display-mode'), feedSort: document.getElementById('feed-sort-direction'),
    feedFormError: document.getElementById('feed-form-error'), saveFeed: document.getElementById('save-feed-btn'),
	feedSettingsModal: document.getElementById('feed-settings-modal'), feedSettingsForm: document.getElementById('feed-settings-form'),
	feedSettingsName: document.getElementById('feed-settings-name'), feedSettingsURL: document.getElementById('feed-settings-url'),
	feedSettingsDisplay: document.getElementById('feed-settings-display-mode'),
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
	problemFeedsButton: document.getElementById('problem-feeds-btn'), problemFeedsModal: document.getElementById('problem-feeds-modal'),
	problemFeedsList: document.getElementById('problem-feeds-list'), deleteFeedModal: document.getElementById('delete-feed-modal'),
	deleteFeedSummary: document.getElementById('delete-feed-summary'), deleteFeedError: document.getElementById('delete-feed-error'),
	confirmDeleteFeed: document.getElementById('confirm-delete-feed-btn'),
	userList: document.getElementById('user-list'), newUserUsername: document.getElementById('new-user-username'),
	newUserPassword: document.getElementById('new-user-password'), newUserError: document.getElementById('new-user-error'),
	addUserAccount: document.getElementById('add-user-account-btn'),
	accountModal: document.getElementById('account-modal'), accountForm: document.getElementById('account-form'),
	accountUsername: document.getElementById('account-username'), accountCurrentPassword: document.getElementById('account-current-password'),
	accountNewPassword: document.getElementById('account-new-password'), accountConfirmPassword: document.getElementById('account-confirm-password'),
	accountError: document.getElementById('account-error'), saveAccount: document.getElementById('save-account-btn'),
  });
}

document.addEventListener('DOMContentLoaded', async () => {
  cacheElements();
	elements.mobileNavToggle.addEventListener('click', () => toggleMobileMenu('nav'));
	elements.mobileActionsToggle.addEventListener('click', () => toggleMobileMenu('actions'));
	document.getElementById('mobile-nav-close').addEventListener('click', closeMobileMenus);
	elements.mobileMenuBackdrop.addEventListener('click', closeMobileMenus);
	elements.appActions.addEventListener('click', event => {
		if (event.target.closest('button, a')) closeMobileMenus();
	});
	elements.subscriptionList.addEventListener('click', event => {
		if (event.target.closest('.group-item, .feed-item')) closeMobileMenus();
	});
	elements.savedViewButton.addEventListener('click', () => {
		closeMobileMenus();
		selectSavedArticles();
	});
	elements.searchViewButton.addEventListener('click', toggleSearch);
	elements.searchForm.addEventListener('submit', event => {
		event.preventDefault();
		submitSearch();
	});
	window.addEventListener('resize', () => {
		if (!mobileMenuIsAvailable()) closeMobileMenus();
	});
  document.getElementById('dismiss-status-btn').addEventListener('click', () => setStatus());
  document.getElementById('add-feed-btn').addEventListener('click', () => {
    setFormError(elements.feedFormError);
    elements.feedModal.showModal();
    elements.feedURL.focus();
  });
	document.getElementById('account-btn').addEventListener('click', openAccount);
	document.getElementById('cancel-account-btn').addEventListener('click', () => elements.accountModal.close());
	elements.accountForm.addEventListener('submit', event => { event.preventDefault(); saveAccount(); });
  document.getElementById('cancel-feed-btn').addEventListener('click', () => elements.feedModal.close());
  elements.feedForm.addEventListener('submit', event => { event.preventDefault(); saveFeed(); });
	elements.feedSettingsButton.addEventListener('click', () => openFeedSettings());
	document.getElementById('cancel-feed-settings-btn').addEventListener('click', () => elements.feedSettingsModal.close());
	elements.feedSettingsForm.addEventListener('submit', event => { event.preventDefault(); saveFeedSettings(); });
	elements.problemFeedsButton.addEventListener('click', openProblemFeeds);
	document.getElementById('close-problem-feeds-btn').addEventListener('click', () => elements.problemFeedsModal.close());
	document.getElementById('cancel-delete-feed-btn').addEventListener('click', () => {
		elements.deleteFeedModal.close();
		state.feedPendingDeletion = null;
		if (failedFeeds().length) openProblemFeeds();
	});
	elements.confirmDeleteFeed.addEventListener('click', deletePendingFeed);
	const settingsButton = document.getElementById('settings-btn');
	settingsButton?.addEventListener('click', async () => {
    setFormError(elements.settingsFormError);
	elements.settingsRefreshResult.hidden = true;
    try {
      await loadSettings();
	  await loadUsers();
      elements.settingsModal.showModal();
    } catch (error) {
      setStatus(`Could not load settings: ${error.message}`, 'error');
    }
  });
	elements.addUserAccount?.addEventListener('click', addUserAccount);
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
  elements.markAllRead.addEventListener('click', requestMarkAllRead);
	document.getElementById('cancel-mark-all-read-btn').addEventListener('click', () => elements.markAllReadModal.close());
	elements.confirmMarkAllRead.addEventListener('click', () => {
		elements.markAllReadModal.close();
		void markAllRead();
	});
	elements.articlePane.addEventListener('scroll', maybeLoadMoreArticles, { passive: true });
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
	startSubscriptionMetadataPolling();
	checkForNewRelease();
});
