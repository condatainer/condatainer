// Groups consecutive code blocks that share a `tabs="<group>"` attribute into one
// `<div class="code-tabs">`. client.js turns each block's title into a tab in the
// frame's header, in the place of the file name.
//
//   ```bash title="src/01_trim.sh" tabs="scripts"
//
// Applies to .mdx pages only.

// Matches `name="value"`, `name='value'` or `name=value` in a code fence's meta.
function attribute(meta, name) {
	const match = new RegExp(`(?:^|\\s)${name}=(?:"([^"]*)"|'([^']*)'|(\\S+))`).exec(meta);
	if (!match) return null;
	return { text: match[0], value: match[1] ?? match[2] ?? match[3] };
}

function groupOf(node) {
	if (node.type !== 'code' || !node.meta) return null;
	return attribute(node.meta, 'tabs');
}

// Drops the `tabs` attribute, and gives a block without a title its language as one.
function prepare(node, tabs) {
	let meta = node.meta.replace(tabs.text, '');
	if (!attribute(meta, 'title')) meta += ` title="${node.lang ?? 'code'}"`;
	node.meta = meta.trim();
}

function wrapper(children) {
	return {
		type: 'mdxJsxFlowElement',
		name: 'div',
		attributes: [{ type: 'mdxJsxAttribute', name: 'className', value: 'code-tabs' }],
		children,
	};
}

// Replaces each run of grouped code blocks among `parent`'s children.
function group(parent) {
	const children = [];
	for (let index = 0; index < parent.children.length; index++) {
		const node = parent.children[index];
		const first = groupOf(node);
		if (!first) {
			if (node.children) group(node);
			children.push(node);
			continue;
		}
		const members = [];
		for (; index < parent.children.length; index++) {
			const member = parent.children[index];
			const tabs = groupOf(member);
			if (!tabs || tabs.value !== first.value) break;
			prepare(member, tabs);
			members.push(member);
		}
		index--;
		children.push(wrapper(members));
	}
	parent.children = children;
}

export default function codeTabs() {
	return (tree, file) => {
		if (String(file.path ?? '').endsWith('.mdx')) group(tree);
	};
}
