#!/usr/bin/env node
// Renders the README's images into .github/readme/: the product visuals
// are captured from https://echowa.dev, so they match the website, and the
// banner and social preview are drawn from hero.html around them.
//
//   npm exec --yes --package=playwright@1.58.0 -- node scripts/readme/render.cjs
//
// The first run needs Playwright's Chromium (`npx playwright@1.58.0
// install chromium`), or CHROME_PATH for another Chrome binary. SITE_URL
// captures another copy of the site, such as the website's dev server.

const { execSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '../..');
const out = path.join(root, '.github/readme');
const site = process.env.SITE_URL || 'https://echowa.dev';

// npm exec puts Playwright's bin on PATH but not its package on the
// require path, so find the package from the bin.
function playwright() {
	try {
		return require('playwright');
	} catch {
		const bin = execSync('command -v playwright', { encoding: 'utf8' }).trim();
		return require(path.join(path.dirname(bin), '..', 'playwright'));
	}
}

// Each capture is an element on the home page, written as <name>.png.
const captures = [
	{ name: 'product', selector: '.hero__product > *', width: 1280 },
	{ name: 'deploys', selector: '#deploys .showcase' },
	{ name: 'databases', selector: '#databases .showcase' },
	{ name: 'metrics', selector: '#metrics .showcase' },
	{ name: 'operations', selector: '#operations .showcase' },
	{ name: 'architecture', selector: '#architecture .architecture__diagram' },
	{ name: 'install', selector: '#install .terminal' },
];

// Still frames: no entrance animations, and nothing behind the cards, so
// the images sit on GitHub's light and dark themes alike.
const still = `
	*, *::before, *::after { animation: none !important; transition: none !important; }
	.rise { opacity: 1 !important; transform: none !important; }
	html, body, .section { background: transparent !important; }
`;

async function main() {
	const { chromium } = playwright();
	const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || undefined });
	fs.mkdirSync(out, { recursive: true });

	for (const { name, selector, width = 1200 } of captures) {
		const page = await browser.newPage({ viewport: { width, height: 1000 }, deviceScaleFactor: 2 });
		await page.goto(site, { waitUntil: 'networkidle' });
		await page.addStyleTag({ content: still });
		await page.evaluate(() => document.fonts.ready);
		const element = page.locator(selector).first();
		await element.scrollIntoViewIfNeeded();
		await page.waitForTimeout(600);
		await element.screenshot({ path: path.join(out, `${name}.png`), omitBackground: true });
		await page.close();
		console.log(`${name}.png`);
	}

	const logo = fs.readFileSync(path.join(root, 'resources/js/Components/Logo.tsx'), 'utf8');
	const wordmark = logo.match(/const WORDMARK =\s*'([^']+)'/)[1];
	const product = `data:image/png;base64,${fs.readFileSync(path.join(out, 'product.png')).toString('base64')}`;

	for (const { name, social } of [{ name: 'banner' }, { name: 'social', social: true }]) {
		const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2 });
		await page.goto(`file://${path.join(__dirname, 'hero.html')}`, { waitUntil: 'networkidle' });
		await page.evaluate(
			({ wordmark, product, social }) => {
				document.getElementById('wordmark').setAttribute('d', wordmark);
				document.getElementById('product').src = product;
				document.body.classList.toggle('social', social);
				return Promise.all([document.fonts.ready, document.getElementById('product').decode()]);
			},
			{ wordmark, product, social: Boolean(social) },
		);
		await page.locator('.banner').screenshot({ path: path.join(out, `${name}.png`), omitBackground: true });
		await page.close();
		console.log(`${name}.png`);
	}

	fs.rmSync(path.join(out, 'product.png'));
	await browser.close();
}

main().catch((error) => {
	console.error(error);
	process.exit(1);
});
