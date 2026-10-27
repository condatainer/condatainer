import { fileURLToPath } from 'node:url';

const file = (name) => fileURLToPath(new URL(name, import.meta.url));

/**
 * Starlight plugin: lists the current page's headings in the left sidebar, under
 * the page's own entry, and removes the right-hand "On this page" column.
 *
 * `minHeadingLevel` and `maxHeadingLevel` set which headings are listed. A page
 * overrides them with `tableOfContents: { minHeadingLevel, maxHeadingLevel }` in
 * its frontmatter, or turns the list off with `tableOfContents: false`.
 */
export default function sidebarToc({ minHeadingLevel = 2, maxHeadingLevel = 3 } = {}) {
	return {
		name: 'sidebar-toc',
		hooks: {
			'config:setup'({ config, logger, updateConfig }) {
				const components = { ...config.components };
				for (const [slot, source] of [
					['Sidebar', 'Sidebar.astro'],
					['PageSidebar', 'PageSidebar.astro'],
				]) {
					if (components[slot]) {
						logger.warn(`A <${slot}> override already exists; sidebar-toc leaves it in place.`);
						continue;
					}
					components[slot] = file(source);
				}
				updateConfig({
					components,
					tableOfContents: config.tableOfContents ?? { minHeadingLevel, maxHeadingLevel },
					customCss: [...(config.customCss ?? []), file('sidebar-toc.css')],
				});
			},
		},
	};
}
