// Turns each `.code-tabs` group into tabs in the code frame's header.
// Without this script every block shows, each with its own file name.
document.addEventListener('DOMContentLoaded', () => {
	for (const group of document.querySelectorAll('.code-tabs')) {
		const frames = [...group.querySelectorAll(':scope > .expressive-code')];
		const names = frames.map((frame) => frame.querySelector('.title')?.textContent ?? '');

		const select = (active) => {
			frames.forEach((frame, index) => {
				frame.style.display = index === active ? '' : 'none';
			});
		};

		frames.forEach((frame, index) => {
			const header = frame.querySelector('figcaption.header');
			if (!header) return;
			header.setAttribute('role', 'tablist');
			header.replaceChildren(
				...names.map((name, other) => {
					const tab = document.createElement(other === index ? 'span' : 'button');
					tab.textContent = name;
					tab.setAttribute('role', 'tab');
					tab.setAttribute('aria-selected', String(other === index));
					if (other === index) {
						tab.className = 'title';
					} else {
						tab.type = 'button';
						tab.className = 'code-tabs-tab';
						tab.addEventListener('click', () => select(other));
					}
					return tab;
				}),
			);
		});
		select(0);
	}
});
