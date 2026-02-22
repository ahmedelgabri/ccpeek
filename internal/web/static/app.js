// Client-side search: filters list items by data-search-keys attribute.
document.querySelectorAll('.search-input').forEach(function (input) {
	var targetClass = input.getAttribute('data-search-target')
	if (!targetClass) return

	var container = input.parentElement.querySelector('.' + targetClass)
	if (!container) return

	var countEl = input.parentElement.querySelector('.search-count')
	var items = container.querySelectorAll('[data-search-keys]')

	function update() {
		var query = input.value.toLowerCase().trim()
		var terms = query ? query.split(/\s+/) : []
		var shown = 0

		items.forEach(function (item) {
			var keys = (item.getAttribute('data-search-keys') || '').toLowerCase()
			var match =
				terms.length === 0 ||
				terms.every(function (t) {
					return keys.indexOf(t) !== -1
				})
			item.hidden = !match
			if (match) shown++
		})

		if (countEl) {
			countEl.textContent =
				terms.length > 0 ? shown + ' of ' + items.length + ' items' : items.length + ' items'
		}
	}

	input.addEventListener('input', update)
	update()
})
