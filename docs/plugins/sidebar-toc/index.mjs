import { fileURLToPath } from 'node:url';

const file = (name) => fileURLToPath(new URL(name, import.meta.url));

/**
 * Starlight plugin: lists the current page's headings in the left sidebar, under
 * the page's own entry, and removes the right-hand "On this page" column.
 *
 * `minHeadingLevel` and `maxHeadingLevel` set which headings are listed. A page
 * overrides them with `tableOfContents: { minHeadingLevel, maxHeadingLevel }` in
 * its frontmatter, or turns the list off with `tableOfContents: false`.
 *
 * A button beside the page's entry shows or hides the list. The list starts open.
 * `collapsed` names the pages whose list starts hidden: a page id such as
 * `reference/cli`, or a prefix that ends in `/` such as `reference/`. A reader's
 * own choice on a page is kept in the browser and wins over that default.
 */
export default function sidebarToc({ minHeadingLevel = 2, maxHeadingLevel = 3, collapsed = [] } = {}) {
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
					head: [...(config.head ?? []), { tag: 'meta', attrs: { name: 'sidebar-toc-collapsed', content: collapsed.join(',') } }],
					customCss: [...(config.customCss ?? []), file('sidebar-toc.css')],
				});
			},
		},
	};
}
