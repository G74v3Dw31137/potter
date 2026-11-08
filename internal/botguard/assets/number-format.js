// Restricted adapter for LinkeDOM's CSS formatter; not a general Intl polyfill.
var Intl={NumberFormat:class NumberFormat {
 constructor(locale,o){if(locale!=='en-US'||o.useGrouping!==false||o.minimumFractionDigits!==0||o.maximumFractionDigits!==6)throw new RangeError('unsupported Intl.NumberFormat profile')}
 format(n){return Number(n).toFixed(6).replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'')}
}};
