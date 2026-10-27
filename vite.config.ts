import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import laravel from 'laravel-vite-plugin';
import { defineConfig, type Plugin } from 'vite';

// laravel-vite-plugin isn't Laravel-specific: it writes public/hot while the
// dev server runs and builds to public/build, which app/inertia/vite.go reads.
export default defineConfig({
    plugins: [
        laravel({
            input: ['resources/js/app.tsx'],
            refresh: false,
        }),
        react(),
        tailwindcss(),
        bundledLicenses(),
    ],
});

// Writes dist/npm-licenses.json: each npm package in the build, with its
// licence files, for scripts/licenses (ADR 0033). Tailwind inlines app.css's
// @imports itself, so Vite never sees those packages as modules.
function bundledLicenses(): Plugin {
    return {
        name: 'echowa-bundled-licenses',
        apply: 'build',
        generateBundle() {
            const roots = new Set<string>();
            for (const id of this.getModuleIds()) {
                // Plugins' virtual modules start with \0 and may add a ?query.
                const file = id.replace(/^\0/, '').split('?')[0];
                const at = file.lastIndexOf('/node_modules/') + '/node_modules/'.length;
                if (at >= '/node_modules/'.length) {
                    roots.add(packageRoot(file.slice(0, at), file.slice(at)));
                }
            }
            const css = readFileSync('resources/css/app.css', 'utf8');
            for (const [, spec] of css.matchAll(/@import '([^.'][^']*)'/g)) {
                roots.add(packageRoot(`${process.cwd()}/node_modules/`, spec));
            }

            const packages = [...roots].map((root) => {
                const pkg = JSON.parse(readFileSync(`${root}/package.json`, 'utf8'));
                // Below the root too: victory-vendor keeps the d3 licences of
                // the code it vendors in lib-vendor/<name>/LICENSE.
                const files = readdirSync(root, { recursive: true, withFileTypes: true })
                    .filter(
                        (entry) =>
                            entry.isFile() &&
                            /^(licen[cs]e|copying|notice)([.\-_].*)?$/i.test(entry.name) &&
                            !relative(root, entry.parentPath).split(sep).includes('node_modules'),
                    )
                    .map((entry) => {
                        const name = relative(root, join(entry.parentPath, entry.name));
                        return { name, text: readFileSync(join(root, name), 'utf8') };
                    })
                    .sort((a, b) => a.name.localeCompare(b.name));
                const license = typeof pkg.license === 'object' ? pkg.license?.type : pkg.license;
                return { name: pkg.name, version: pkg.version, license: license ?? '', files };
            });
            packages.sort((a, b) => a.name.localeCompare(b.name) || a.version.localeCompare(b.version));

            mkdirSync('dist', { recursive: true });
            writeFileSync('dist/npm-licenses.json', `${JSON.stringify(packages, null, 2)}\n`);
        },
    };
}

// The package directory holding a module, given the node_modules directory
// and the path below it: `@scope/name` or `name`.
function packageRoot(nodeModules: string, rest: string): string {
    const parts = rest.split('/');
    return nodeModules + parts.slice(0, parts[0].startsWith('@') ? 2 : 1).join('/');
}
