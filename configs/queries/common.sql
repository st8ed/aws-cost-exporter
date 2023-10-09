select
    (`bill/BillingPeriodStartDate` || "-" || `bill/BillingPeriodEndDate`)  as `period`,

    `product/ProductName` as `product`,
    COALESCE(`lineItem/Operation`, "") as `operation`,
    `lineItem/LineItemType` as `item_type`,

    COALESCE(`product/usagetype`, "") as `usage_type`,
    COALESCE(`pricing/unit`, "") as `usage_unit`,
    SUM(`lineItem/UsageAmount`) as metric_amount,

    SUM(`lineItem/UnblendedCost`) as metric_cost,
    `lineItem/CurrencyCode` as `currency`
from `report-current.csv`
where `lineItem/UnblendedCost` > 0
group by
    `bill/BillingPeriodStartDate`,
    `bill/BillingPeriodEndDate`,
    `product/ProductName`,
    `lineItem/Operation`,
    `lineItem/LineItemType`,

    `product/usagetype`,
    `pricing/unit`,
    `lineItem/CurrencyCode`
order by `period`, `product`, `operation`
