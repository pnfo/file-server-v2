// `import url from '...?url'`: scripts/build-assets.mjs copies the file to /fs-assets/ and exports its URL.
declare module '*?url' {
  const url: string
  export default url
}
