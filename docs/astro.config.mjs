// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import { readFileSync } from 'node:fs';
import { unified } from '@astrojs/markdown-remark';
import sidebarToc from './plugins/sidebar-toc/index.mjs';
import codeTabs from './plugins/code-tabs/index.mjs';
import relativeMdLinks from 'astro-rehype-relative-markdown-links';

// The address the site is served at: Read the Docs sets it for a tag, e.g.
// /en/v1.2.0/, and SITE_URL for anything else. Unset, the site is served at /.
const url = process.env.READTHEDOCS_CANONICAL_URL ?? process.env.SITE_URL;
const base = url ? new URL(url).pathname : '/';

export default defineConfig({
	site: url ? new URL(url).origin : undefined,
	base,
	trailingSlash: 'always',
	// IPv4 loopback: `localhost` binds only ::1, which an IPv4 proxy cannot reach.
	server: { host: '127.0.0.1' },
	// Turns relative links between pages (../guide/page.md#heading) into site URLs.
	markdown: {
		processor: unified({
			remarkPlugins: [codeTabs],
			rehypePlugins: [[relativeMdLinks, { collectionBase: false, base, trailingSlash: 'always' }]],
		}),
	},
	integrations: [
		starlight({
			title: 'CondaTainer',
			logo: { src: './src/assets/logo_cnt.svg', alt: 'CondaTainer', replacesTitle: true },
			favicon: '/favicon.png',
			head: [{ tag: 'script', content: readFileSync(new URL('./plugins/code-tabs/client.js', import.meta.url), 'utf8') }],
			customCss: ['./src/styles/custom.css'],
				components: { Header: './src/components/Header.astro', Hero: './src/components/Hero.astro' },
			plugins: [sidebarToc({ minHeadingLevel: 2, maxHeadingLevel: 3 })],
			expressiveCode: { defaultProps: { frame: 'code' }, frames: { extractFileNameFromCode: false } },
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/condatainer/condatainer' }],
			editLink: { baseUrl: 'https://github.com/condatainer/condatainer/edit/main/docs/' },
			sidebar: [
				{
					label: 'Get Started',
					items: [
						{ label: 'Installation', slug: 'get_started/installation' },
						{ label: 'Quick Start', slug: 'get_started/quick_start' },
					],
				},
				{
					label: 'User Guides',
					items: [
						{ label: 'Concept', slug: 'guides/concept' },
						{ label: 'Module Overlay', slug: 'guides/overlays' },
						{ label: 'Environment Overlay', slug: 'guides/environments' },
						{ label: 'Identity and Equivalence', slug: 'guides/identity' },
						{ label: 'Scheduler', slug: 'guides/scheduler' },
						{ label: 'Proxy', slug: 'guides/proxy' },
						{ label: 'Helper', slug: 'guides/helper' },
						{ label: 'Project', slug: 'guides/project' },
						{ label: 'Layers and Store', slug: 'guides/layers' },
						{ label: 'Configuration', slug: 'guides/config' },
					],
				},
				{
					label: 'Troubleshooting',
					items: [
						{ label: 'Environment Overlay Issues', slug: 'troubleshooting/environment_overlay' },
						{ label: 'Helper Issues', slug: 'troubleshooting/helper' },
					],
				},
				{
					label: 'Advanced',
					items: [
						{ label: 'MPI (Experimental)', slug: 'advanced/mpi' },
						{ label: 'Custom Build Recipe', slug: 'advanced/custom_recipe' },
						{ label: 'Custom Helper', slug: 'advanced/custom_helper' },
						{ label: 'Custom Recipe Source', slug: 'advanced/custom_source' },
					],
				},
				{
					label: 'Deployment',
					items: [
						{ label: 'Group Install', slug: 'deployment/group_install' },
						{ label: 'Site Install', slug: 'deployment/site_install' },
						{ label: 'Site Install with Modules', slug: 'deployment/site_modules' },
					],
				},
			],
		}),
	],
});
