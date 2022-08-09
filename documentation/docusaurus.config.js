const path = require('path');

module.exports = {
    plugins: [
        [
            '@docusaurus/plugin-content-docs',
            {
                id: 'inx-tendercoo-develop',
                path: path.resolve(__dirname, 'docs'),
                routeBasePath: 'inx-tendercoo',
                sidebarPath: path.resolve(__dirname, 'sidebars.js'),
                editUrl: 'https://github.com/iotaledger/inx-tendercoo/edit/develop/documentation',
                versions: {
                    current: {
                        label: 'Develop',
                        path: 'develop',
                        badge: true
                    },
                },
            }
        ],
    ],
    staticDirectories: [path.resolve(__dirname, 'static')],
};
