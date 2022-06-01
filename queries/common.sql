select
    (`bill/BillingPeriodStartDate` || `bill/BillingPeriodEndDate`)  as `period`,

    `product/ProductName` as `product`,
    `lineItem/Operation` as `operation`,
    `lineItem/LineItemType` as `item_type`,

    `lineItem/UsageType` as `usage_type`,
    `pricing/unit` as `usage_unit`,
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

    `lineItem/UsageType`,
    `pricing/unit`,
    `lineItem/CurrencyCode`
order by `period`, `product`, `operation`
